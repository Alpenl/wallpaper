#!/usr/bin/env python3
"""测某进程树在 N 秒内的 CPU 核数和合计 PSS 内存（Linux /proc）。用法: measure_tree.py <root_pid> <seconds>"""
import os, sys, time
def tree(root):
    kids={}; 
    for p in os.listdir('/proc'):
        if p.isdigit():
            try: pp=int(open(f'/proc/{p}/stat').read().rsplit(')',1)[1].split()[1]); kids.setdefault(pp,[]).append(int(p))
            except Exception: pass
    out=[root]; i=0
    while i<len(out): out+=kids.get(out[i],[]); i+=1
    return out
def cpu(pids):
    s=0
    for p in pids:
        try: f=open(f'/proc/{p}/stat').read().rsplit(')',1)[1].split(); s+=int(f[11])+int(f[12])
        except Exception: pass
    return s/os.sysconf('SC_CLK_TCK')
def pss(pids):
    s=0
    for p in pids:
        try:
            for line in open(f'/proc/{p}/smaps_rollup'):
                if line.startswith('Pss:'): s+=int(line.split()[1])
        except Exception: pass
    return s/1024
root=int(sys.argv[1]); dur=float(sys.argv[2])
p=tree(root); c0=cpu(p); t0=time.time(); time.sleep(dur); p=tree(root); c1=cpu(p)
print(f"procs={len(p)} cpu_cores={(c1-c0)/(time.time()-t0):.3f} pss_MiB={pss(p):.1f}")
