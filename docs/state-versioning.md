# State file versioning

Baseplate's state file carries a `version` field. This build reads and writes **version 1**.

- A state file written by a **newer** Baseplate (higher `version`) is refused with a clear message —
  upgrade Baseplate rather than risk corrupting newer-format state with an older binary.
- When the on-disk format changes in future, Baseplate bumps the version and ships a **migration**
  that upgrades older state files automatically on read.
- The same versioned document is used by both the local file backend and the HTTP remote backend.

You don't normally edit or think about this — it exists so state files stay safe across upgrades.
