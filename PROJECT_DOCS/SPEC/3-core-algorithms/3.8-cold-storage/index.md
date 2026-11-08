### 3.8 Cold storage

Closed threads are moved to **cold storage** — archived out of the thread registry under thread
registry size pressure and restored with `personant archive recover`; an archived thread's bytes are kept in the
history repository. **Size pressure** is the registry holding more records than
an upper bound; archival then drains the coldest closed threads until the registry is down to a
lower bound, the **low-water mark**.

Contents:

- [§3.8 Runs only at day rollover](runs-only-at-day-rollover.md)
- [§3.8.1 Mechanism](3.8.1-mechanism.md)
- [§3.8.2 Storage properties](3.8.2-storage-properties.md)
- [§3.8.3 Restore](3.8.3-restore.md)
- [§3.8.4 Recall treatment of archived threads (off-recall)](3.8.4-recall-treatment-of-archived-threads-off-recall.md)
