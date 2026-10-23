# State file versioning

Baseplate's state file carries a `version` field. This build reads and writes **version 2**.

| Version | Change |
|---|---|
| 1 | Initial format. |
| 2 | Resources may carry an `index_key`: the instance key of a `for_each` resource. Version 1 files are upgraded automatically on read (a v1 resource is simply singular). The bump exists so an older Baseplate refuses a file with keyed instances instead of merging them onto one address. |

- A state file written by a **newer** Baseplate (higher `version`) is refused with a clear message —
  upgrade Baseplate rather than risk corrupting newer-format state with an older binary.
- When the on-disk format changes in future, Baseplate bumps the version and ships a **migration**
  that upgrades older state files automatically on read.
- The same versioned document is used by both the local file backend and the HTTP remote backend.

You don't normally edit or think about this — it exists so state files stay safe across upgrades.
