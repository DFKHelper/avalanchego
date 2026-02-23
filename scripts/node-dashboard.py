#!/usr/bin/env python3
"""
AvalancheGo Node Dashboard
Live terminal dashboard showing all chain sync status.
Usage: python3 node-dashboard.py
"""

import re
import time
import subprocess
import urllib.request
import urllib.error
import json
from datetime import datetime, timedelta
from collections import deque

from rich.console import Console
from rich.layout import Layout
from rich.live import Live
from rich.panel import Panel
from rich.table import Table
from rich.text import Text
from rich.progress import BarColumn, Progress, TextColumn, TimeRemainingColumn
from rich import box

# ── Config ──────────────────────────────────────────────────────────────────
METRICS_URL  = "http://localhost:9650/ext/metrics"
INFO_URL     = "http://localhost:9650/ext/info"
REFRESH_SECS = 4
LOG_DIR      = "/root/.avalanchego/logs"

# Known chain labels (short name, log file suffix)
CHAIN_NAMES = {
    "P": ("P-Chain",  "P.log"),
    "q2aTwKuyzgs8pynF7UXBZCU7DejbZbZ6EUyHr3JQzYgwNPUPi": (
        "DFK Chain", "q2aTwKuyzgs8pynF7UXBZCU7DejbZbZ6EUyHr3JQzYgwNPUPi.log"),
    "C": ("C-Chain",  "C.log"),
    "X": ("X-Chain",  "X.log"),
}

# Known subnet labels
SUBNET_NAMES = {
    "Vn3aX6hNRstj5VHHm63TCgPNaeGnRSqCYXQqemSqDd2TQH4qJ": "DFK Subnet",
}

# ── Metric parsing ───────────────────────────────────────────────────────────

def fetch_text(url, timeout=4):
    try:
        with urllib.request.urlopen(url, timeout=timeout) as r:
            return r.read().decode()
    except Exception:
        return ""

def parse_metrics(raw):
    """Return dict: metric_name{labels} -> float value."""
    result = {}
    for line in raw.splitlines():
        if line.startswith("#") or not line.strip():
            continue
        m = re.match(r'^(\S+?)(\{[^}]*\})?\s+(\S+)', line)
        if not m:
            continue
        key = m.group(1) + (m.group(2) or "")
        try:
            value = float(m.group(3))
        except ValueError:
            continue  # skip +Inf, NaN, etc.
        result[key] = value
    return result

def get_label(metric_key, label):
    """Extract a label value from a metric key like foo{chain="P",bar="x"}."""
    m = re.search(rf'{label}="([^"]*)"', metric_key)
    return m.group(1) if m else None

def metric(metrics, name, **labels):
    """Look up a metric by name + label dict."""
    label_str = ",".join(f'{k}="{v}"' for k, v in labels.items())
    key = f'{name}{{{label_str}}}'
    if key in metrics:
        return metrics[key]
    # Try partial match (label ordering may differ)
    for k, v in metrics.items():
        if k.startswith(name + "{") or k == name:
            if all(f'{lk}="{lv}"' in k for lk, lv in labels.items()):
                return v
    return None

def find_metric_prefix(metrics, prefix, **labels):
    """Find first metric whose name starts with prefix and matches labels."""
    for k, v in metrics.items():
        if k.startswith(prefix):
            if all(f'{lk}="{lv}"' in k for lk, lv in labels.items()):
                return v
    return None

# ── Log parsing ──────────────────────────────────────────────────────────────

def tail_log(path, lines=200):
    try:
        result = subprocess.run(
            ["tail", f"-{lines}", path],
            capture_output=True, text=True, timeout=3)
        return result.stdout
    except Exception:
        return ""

def parse_tries_from_log(log_text):
    """Return (tries_remaining, eta_str) from the most recent log line."""
    best = None
    for line in log_text.splitlines():
        m = re.search(r'triesRemaining=(\d+)\s+ETA=(\S+)', line)
        if m:
            best = (int(m.group(1)), m.group(2))
    return best  # (tries_remaining, eta_str) or None

def parse_starting_tries_from_log(log_text):
    """Find the maximum triesRemaining ever seen (initial total)."""
    vals = [int(m.group(1)) for m in
            re.finditer(r'triesRemaining=(\d+)', log_text)]
    return max(vals) if vals else None

# ── Node info ────────────────────────────────────────────────────────────────

def get_node_info():
    raw = fetch_text(INFO_URL, timeout=3)
    if not raw:
        return {}
    try:
        data = json.loads(raw)
        return data.get("result", {})
    except Exception:
        return {}

# ── State machine ────────────────────────────────────────────────────────────

class ChainState:
    def __init__(self, chain_id):
        self.chain_id      = chain_id
        self.name, _       = CHAIN_NAMES.get(chain_id, (chain_id[:8] + "...", None))
        self.status        = "unknown"     # synced | state-sync | bootstrapping | unknown
        self.height        = None
        self.leafs         = None
        self.leafs_per_sec = None
        self.tries_rem     = None
        self.tries_total   = None
        self.eta_str       = None
        self.frozen        = False         # True if triesRemaining not moving
        self._prev_tries   = deque(maxlen=5)
        self.peers         = None

    def update(self, metrics, log_text=""):
        chain = self.chain_id

        # Bootstrap finished?
        bf = metric(metrics, "avalanche_snowman_bootstrap_finished", chain=chain)

        # State sync metrics (EVM chains expose these via a VM-prefixed metric)
        total_leafs = find_metric_prefix(
            {k: v for k, v in metrics.items() if "state_sync_total_leafs" in k},
            "avalanche_", **{"chain": chain})
        leafs_per_sec = find_metric_prefix(
            {k: v for k, v in metrics.items() if "state_sync_leafs_per_second" in k},
            "avalanche_", **{"chain": chain})

        height = metric(metrics, "avalanche_snowman_last_accepted_height", chain=chain)

        if bf == 1.0:
            self.status = "synced"
            self.height = int(height) if height is not None else None
        elif total_leafs is not None:
            self.status = "state-sync"
            self.leafs  = int(total_leafs)
            self.leafs_per_sec = int(leafs_per_sec) if leafs_per_sec else 0
            # Parse tries from log
            parsed = parse_tries_from_log(log_text)
            if parsed:
                tries, eta = parsed
                self._prev_tries.append(tries)
                self.tries_rem = tries
                self.frozen = len(self._prev_tries) >= 3 and len(set(self._prev_tries)) == 1
                # Only show ETA from log when not obviously runaway
                # Log ETA climbs every minute when frozen, so cap display
                if not self.frozen:
                    self.eta_str = eta
                else:
                    self.eta_str = None  # will be shown as "large trie..."
                if self.tries_total is None:
                    total = parse_starting_tries_from_log(log_text)
                    if total:
                        self.tries_total = total
        elif bf == 0.0:
            self.status = "bootstrapping"
            self.height = int(height) if height is not None else None
        else:
            self.status = "unknown"

        # Subnet peers
        for k, v in metrics.items():
            if "network_peers_subnet" in k:
                # match all chains in this subnet — just grab any subnet peer count
                pass

# ── Rendering ────────────────────────────────────────────────────────────────

STATUS_STYLE = {
    "synced":       ("[green]✓ synced[/green]",    "green"),
    "state-sync":   ("[yellow]⟳ state-sync[/yellow]", "yellow"),
    "bootstrapping":("[blue]↓ bootstrap[/blue]",   "blue"),
    "unknown":      ("[dim]? unknown[/dim]",        "dim"),
}

def fmt_num(n):
    if n is None:
        return "—"
    if n >= 1_000_000:
        return f"{n/1_000_000:.2f}M"
    if n >= 1_000:
        return f"{n:,}"
    return str(n)

def make_header(node_info, metrics, elapsed_str):
    version = node_info.get("version", "?")
    peers   = int(metrics.get("avalanche_network_peers", 0))
    now     = datetime.now().strftime("%H:%M:%S")
    t = Table.grid(expand=True)
    t.add_column(justify="left")
    t.add_column(justify="right")
    subnet_peers = next(
        (int(v) for k, v in metrics.items() if "network_peers_subnet" in k), None)
    subnet_str = f"  [dim]DFK subnet peers: {subnet_peers}[/dim]" if subnet_peers is not None else ""
    t.add_row(
        f"[bold cyan]AvalancheGo[/bold cyan]  [dim]{version}[/dim]  "
        f"[white]Peers: [bold]{peers}[/bold][/white]{subnet_str}",
        f"[dim]Updated {now}  dashboard up {elapsed_str}[/dim]"
    )
    return Panel(t, box=box.ROUNDED, style="bold")

def make_chain_table(chains):
    t = Table(box=box.SIMPLE_HEAD, expand=True, show_header=True,
              header_style="bold cyan")
    t.add_column("Chain",    min_width=12)
    t.add_column("Status",   min_width=14)
    t.add_column("Height / Progress", min_width=22)
    t.add_column("Rate",     min_width=12)
    t.add_column("Notes",    min_width=16)

    for cs in chains:
        label, style = STATUS_STYLE.get(cs.status, STATUS_STYLE["unknown"])

        if cs.status == "synced":
            progress_str = fmt_num(cs.height)
            rate_str     = "—"
            notes_str    = ""
        elif cs.status == "state-sync":
            if cs.tries_rem is not None:
                pct = ""
                if cs.tries_total:
                    done = cs.tries_total - cs.tries_rem
                    pct  = f" ({100*done//cs.tries_total}%)"
                progress_str = f"tries: {cs.tries_rem:,}{pct}\nleafs: {fmt_num(cs.leafs)}"
            else:
                progress_str = f"leafs: {fmt_num(cs.leafs)}"
            rate_str = f"{fmt_num(cs.leafs_per_sec)}/s" if cs.leafs_per_sec else "—"
            if cs.frozen:
                notes_str = "[dim]large trie...[/dim]"
            elif cs.eta_str:
                notes_str = f"ETA {cs.eta_str}"
            else:
                notes_str = ""
        elif cs.status == "bootstrapping":
            progress_str = fmt_num(cs.height) if cs.height else "—"
            rate_str     = "—"
            notes_str    = ""
        else:
            progress_str = "—"
            rate_str     = "—"
            notes_str    = ""

        t.add_row(
            f"[bold]{cs.name}[/bold]",
            Text.from_markup(label),
            progress_str,
            rate_str,
            Text.from_markup(notes_str) if notes_str else "",
        )

    return Panel(t, title="[bold]Chains[/bold]", box=box.ROUNDED)

def make_sync_panel(chains):
    """Detailed progress panel for any chain in state-sync."""
    syncing = [c for c in chains if c.status == "state-sync" and c.tries_total]
    if not syncing:
        return None

    rows = []
    for cs in syncing:
        done  = cs.tries_total - cs.tries_rem
        pct   = done / cs.tries_total
        bar_w = 38
        filled = int(bar_w * pct)
        bar    = "█" * filled + "░" * (bar_w - filled)

        if cs.frozen:
            eta_disp = "[dim]processing large trie (triesRemaining frozen)[/dim]"
        elif cs.eta_str:
            eta_disp = f"ETA [bold]{cs.eta_str}[/bold]"
        else:
            eta_disp = ""

        rows.append(
            f"[bold]{cs.name}[/bold] tries\n"
            f"[green]{bar}[/green] [bold]{pct*100:.0f}%[/bold]  "
            f"{done:,} / {cs.tries_total:,} completed  {eta_disp}\n"
            f"[dim]Leafs this session: {fmt_num(cs.leafs)}   "
            f"Rate: {fmt_num(cs.leafs_per_sec)}/s[/dim]"
        )

    content = "\n\n".join(rows)
    return Panel(Text.from_markup(content),
                 title="[bold]State Sync Progress[/bold]",
                 box=box.ROUNDED)

# ── Main loop ────────────────────────────────────────────────────────────────

def build_layout(chains, node_info, metrics, elapsed_str):
    panels = [make_header(node_info, metrics, elapsed_str)]
    panels.append(make_chain_table(chains))
    detail = make_sync_panel(chains)
    if detail:
        panels.append(detail)
    # Stack vertically
    from rich.columns import Columns
    from rich import print as rprint
    layout = Layout()
    layout.split_column(*[Layout(p, size=None) for p in panels])
    return layout

def main():
    console = Console()
    start   = time.time()
    chains  = {}   # chain_id -> ChainState

    # Pre-read log file for tries_total detection (reads more lines once)
    dfk_id  = "q2aTwKuyzgs8pynF7UXBZCU7DejbZbZ6EUyHr3JQzYgwNPUPi"
    dfk_log = f"{LOG_DIR}/{dfk_id}.log"

    with Live(console=console, refresh_per_second=1, screen=True) as live:
        while True:
            raw_metrics = fetch_text(METRICS_URL)
            metrics     = parse_metrics(raw_metrics)
            node_info   = get_node_info()

            # Discover chains from bootstrap_finished metrics
            for key in metrics:
                if "snowman_bootstrap_finished{" in key:
                    cid = get_label(key, "chain")
                    if cid and cid not in chains:
                        chains[cid] = ChainState(cid)

            # Update each chain
            for cid, cs in chains.items():
                log_text = ""
                _, log_file = CHAIN_NAMES.get(cid, ("", None))
                if log_file:
                    log_text = tail_log(f"{LOG_DIR}/{log_file}", lines=300)
                cs.update(metrics, log_text)

            # Elapsed uptime since dashboard start (node uptime not exposed simply)
            elapsed = int(time.time() - start)
            h, rem  = divmod(elapsed, 3600)
            m, s    = divmod(rem, 60)
            elapsed_str = f"{h}h{m:02d}m" if h else f"{m}m{s:02d}s"

            # Build display
            chain_list = sorted(chains.values(),
                                key=lambda c: (c.status != "synced", c.name))

            from rich.console import Group
            panels = [make_header(node_info, metrics, elapsed_str),
                      make_chain_table(chain_list)]
            detail = make_sync_panel(chain_list)
            if detail:
                panels.append(detail)

            live.update(Group(*panels))
            time.sleep(REFRESH_SECS)

if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
