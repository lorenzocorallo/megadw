# Technical notes

## Download and recovery model

megadw keeps one job-scoped sparse `.mega.part` file for each remote file.
Parallel ranges write directly into that file, which avoids per-chunk files and
a second full-size merge copy.

Checkpoints persist file data before the compact SQLite resume bitmap. After a
restart, the downloader resumes only ranges recorded as durable. MEGA integrity
verification completes before the partial file is atomically renamed into the
final root on the same filesystem.

Worker counts, buffers, retries, events, file descriptors, and bandwidth are
bounded for small servers. HTTP 509 and other quota responses pause the
selected job and preserve its account/proxy context; identities are not rotated
to evade transfer limits.

## Security model

MEGA credentials and sessions, public-link keys, and proxy passwords are
encrypted at rest with AES-256-GCM. Local administrator session tokens are
stored only as SHA-256 digests, and secret material is redacted from normal
logs.

Browser-supplied paths are relative and confined to configured roots. Existing
symlinks in controlled paths are rejected. Configured roots are held as
directory capabilities, while nested opens, deletion, verification, and
finalization use descriptor-relative Linux operations so replacing a parent
path cannot redirect work outside the root.

See [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) for direct runtime and
build dependencies. The repository also includes dependency-license,
systemd-hardening, production-browser, and Docker smoke checks.

## Resource profiles

The minimum supported host is 2 vCPU and 4 GiB RAM; the upper target is 4 vCPU
and 8 GiB RAM. On the minimum profile, the downloader-only process must remain
below 512 MiB RSS under the default eight-worker stress fixture. Normal idle
and eight-worker targets are 80 MiB and 180 MiB RSS.

Run the deterministic benchmark matrix with:

```bash
make resource-benchmark
```

Use `taskset -c 0-1` for a true two-CPU affinity on larger hosts. For
infrastructure validation, run the benchmark in a cgroup or container with
`CPUQuota=200%` and `MemoryMax=4G`, or the equivalent 4-vCPU/8-GiB limits.

The latest measurements recorded for this checkout were:

| Profile | Workers | Throughput | Max RSS | CPU / target CPUs | Max FDs | Max goroutines | SQLite writes/s | Checkpoints/s | Temp overhead |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| small | 1 | 47.94 MiB/s | 21.24 MiB | 4.96% | 14 | 12 | 0.562 | 0.562 | 0.36 MiB |
| small | 4 | 154.94 MiB/s | 24.29 MiB | 16.95% | 17 | 18 | 0.605 | 0.605 | 0.30 MiB |
| small | 8 | 241.73 MiB/s | 22.91 MiB | 27.38% | 21 | 30 | 0.944 | 0.944 | 0.30 MiB |
| large | 8 | 245.47 MiB/s | 24.22 MiB | 15.82% | 21 | 30 | 0.959 | 0.959 | 0.30 MiB |
| large | 12 | 293.92 MiB/s | 24.86 MiB | 18.37% | 25 | 42 | 1.148 | 1.148 | 0.84 MiB |

This environment allowed CPU affinity but not a writable child memory cgroup,
so its memory values are host measurements rather than constrained-profile
results. The benchmark reports its active constraints and does not label an
unconstrained run as constrained.

Defaults remain 4 workers per file, 2 active files, and 8 global workers.
Change them only after the full matrix shows equivalent throughput with
materially lower resource use.
