## 2. Data model

This section states what a compliant implementation must keep in memory and the properties of that
storage. The storage layout and the record, symbol and log formats this implementation uses are in
ARCHITECTURE "Storage and record formats" and "Event log format"; directive files ([§2.6](2.6-directive-file-format-directives-md/index.md)) stay
here because users edit them.

Contents:

- [§2.1 Storage layout](2.1-storage-layout/index.md)
- [§2.2 Thread registry record schema (`spine.jsonl`)](2.2-thread-registry-record-schema-spine-jsonl/index.md)
- [§2.3 Thread file format (`threads/thr_<id>/`)](2.3-thread-file-format-threads-thr-id.md)
- [§2.4 Symbol index schema (`symbols.jsonl`)](2.4-symbol-index-schema-symbols-jsonl.md)
- [§2.5 Project metadata schema](2.5-project-metadata-schema/index.md)
- [§2.6 Directive file format (`directives/*.md`)](2.6-directive-file-format-directives-md/index.md)
- [§2.7 Symbol normalization and categories](2.7-symbol-normalization-and-categories/index.md)
- [§2.8 Log event format (`logs/YYYY-MM-DD.log`)](2.8-log-event-format-logs-yyyy-mm-dd-log/index.md)
