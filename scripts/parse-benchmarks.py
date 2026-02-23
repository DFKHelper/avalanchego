#!/usr/bin/env python3
"""
Parse Go benchmark output and produce a LevelDB vs PebbleDB vs Firewood comparison table.
Usage: python3 parse-benchmarks.py /root/firewood-pr-data/bench-raw.txt
"""
import sys
import re
from collections import defaultdict

def parse_bench_line(line):
    """Parse a Go benchmark result line."""
    # Format: BenchmarkInterface/leveldb_1024_pairs_32_keys_32_values_Get-16  N  X ns/op  Y B/op  Z allocs/op
    m = re.match(
        r'BenchmarkInterface/(\w+)_(\d+)_pairs_(\d+)_keys_(\d+)_values_(\w+)-\d+\s+'
        r'(\d+)\s+([\d.]+)\s+ns/op\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op',
        line.strip()
    )
    if not m:
        return None
    db, pairs, ksize, vsize, op, iters, ns_op, bop, allocs = m.groups()
    return {
        'db': db,
        'pairs': int(pairs),
        'key_size': int(ksize),
        'val_size': int(vsize),
        'op': op,
        'iters': int(iters),
        'ns_op': float(ns_op),
        'bytes_op': int(bop),
        'allocs_op': int(allocs),
    }

def format_ns(ns):
    if ns >= 1_000_000:
        return f"{ns/1_000_000:.2f} ms"
    elif ns >= 1_000:
        return f"{ns/1_000:.1f} µs"
    else:
        return f"{ns:.0f} ns"

def speedup(base_ns, cmp_ns):
    if cmp_ns == 0:
        return "N/A"
    ratio = base_ns / cmp_ns
    if ratio >= 1:
        return f"{ratio:.1f}x faster"
    else:
        return f"{1/ratio:.1f}x slower"

def main(path):
    results = defaultdict(dict)  # (pairs, key_size, val_size, op) -> {db: ns_op}

    with open(path) as f:
        for line in f:
            r = parse_bench_line(line)
            if r:
                key = (r['pairs'], r['key_size'], r['val_size'], r['op'])
                results[key][r['db']] = r

    dbs = ['leveldb', 'pebbledb', 'firewood']
    ops = ['Get', 'Put', 'Delete', 'BatchWrite', 'BatchPut', 'BatchDelete',
           'ParallelGet', 'ParallelPut', 'ParallelDelete']
    sizes = sorted(set((p,k,v) for (p,k,v,_) in results), key=lambda x: x[1])

    print("=" * 90)
    print("  Database Benchmark Comparison: LevelDB vs PebbleDB vs Firewood")
    print("  Workload: 1024 pre-populated key/value pairs, random keys")
    print("  Hardware: AMD Ryzen 7 7700 (8-core), Linux x86-64")
    print("=" * 90)

    for (pairs, ksize, vsize) in sizes:
        print(f"\n{'─'*90}")
        print(f"  Key={ksize}B, Value={vsize}B, {pairs} pairs")
        print(f"{'─'*90}")
        header = f"  {'Operation':<22} {'LevelDB':>14} {'PebbleDB':>14} {'Firewood':>14}  {'vs LevelDB':>16}"
        print(header)
        print(f"  {'-'*22} {'-'*14} {'-'*14} {'-'*14}  {'-'*16}")

        for op in ops:
            key = (pairs, ksize, vsize, op)
            if key not in results:
                continue
            row = results[key]
            ldb = row.get('leveldb', {}).get('ns_op')
            pdb = row.get('pebbledb', {}).get('ns_op')
            fwd = row.get('firewood', {}).get('ns_op')

            ldb_s = format_ns(ldb) if ldb else "N/A"
            pdb_s = format_ns(pdb) if pdb else "N/A"
            fwd_s = format_ns(fwd) if fwd else "N/A"
            cmp_s = speedup(ldb, fwd) if (ldb and fwd) else "N/A"

            print(f"  {op:<22} {ldb_s:>14} {pdb_s:>14} {fwd_s:>14}  {cmp_s:>16}")

    # Summary
    print(f"\n{'='*90}")
    print("  Summary: Firewood vs LevelDB")
    print(f"{'='*90}")

    fw_wins = []
    ldb_wins = []
    for key, row in results.items():
        ldb = row.get('leveldb', {}).get('ns_op')
        fwd = row.get('firewood', {}).get('ns_op')
        if ldb and fwd:
            pairs, ksize, vsize, op = key
            label = f"{op} ({ksize}B key, {vsize}B val)"
            if fwd < ldb:
                fw_wins.append((ldb/fwd, label))
            else:
                ldb_wins.append((fwd/ldb, label))

    if fw_wins:
        fw_wins.sort(reverse=True)
        print("\n  Firewood faster than LevelDB:")
        for ratio, label in fw_wins[:10]:
            print(f"    {ratio:.1f}x  {label}")

    if ldb_wins:
        ldb_wins.sort(reverse=True)
        print("\n  LevelDB faster than Firewood:")
        for ratio, label in ldb_wins[:10]:
            print(f"    {ratio:.1f}x  {label}")

    avg_fw_vs_ldb = None
    ratios = []
    for key, row in results.items():
        ldb = row.get('leveldb', {}).get('ns_op')
        fwd = row.get('firewood', {}).get('ns_op')
        if ldb and fwd:
            ratios.append(ldb / fwd)
    if ratios:
        avg = sum(ratios) / len(ratios)
        median = sorted(ratios)[len(ratios)//2]
        print(f"\n  Average speedup (Firewood/LevelDB): {avg:.2f}x")
        print(f"  Median speedup  (Firewood/LevelDB): {median:.2f}x")
        note = "faster" if avg > 1 else "slower"
        print(f"  Overall: Firewood is {abs(avg-1)*100:.0f}% {note} than LevelDB on average")

    print(f"\n{'='*90}")
    print("  Notes:")
    print("  - These benchmarks use random key/value pairs (not realistic blockchain data)")
    print("  - Firewood advantage for blockchain: built-in Merkle proofs, no separate trie layer")
    print("  - Firewood advantage: persistent state across restarts (rootStore fix)")
    print("  - Firewood advantage: memory-safe (no unbounded registry growth)")
    print(f"{'='*90}\n")

if __name__ == '__main__':
    path = sys.argv[1] if len(sys.argv) > 1 else '/root/firewood-pr-data/bench-raw.txt'
    main(path)
