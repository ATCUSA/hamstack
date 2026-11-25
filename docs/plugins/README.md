# Plugins (draft)

> **Status: draft.** The manifest format and `hamstack plugin validate` exist and are tested. The part of hamstack that
> installs, runs and shows plugins is **not built yet**. Write to the spec now; expect small changes. Nothing here is
> a promise of a feature that works today.

A plugin is a separate program with its own small web server. hamstack will install it from a GitHub release (checksum
verified), run it as a user service on a localhost port it chooses, and show its page inside the dashboard at
`/plugin/<id>/`, behind the dashboard's login. Plugins are out-of-process on purpose: a crash or a bug in one cannot
take down the dashboard or hold the radio's serial port.

| Read | For |
|---|---|
| [spec.md](spec.md) | Every manifest field, what hamstack guarantees the plugin, and what the plugin must guarantee hamstack |
| [guide.md](guide.md) | Writing one, step by step, with rigctlWeb as the worked example |
| [best-practices.md](best-practices.md) | Radio safety, security, packaging, testing |
| [ai-prompt.md](ai-prompt.md) | A prompt to give an AI coding assistant so what it builds meets the spec |
| [rigctlweb-changes.md](rigctlweb-changes.md) | What the first real plugin candidate must change to fit |
| [examples/rigctlweb.json](examples/rigctlweb.json) | A valid manifest |

Check a manifest: `hamstack plugin validate plugin.json`. The validator is the ground truth: if this text and the
validator disagree, the validator wins and the text has a bug.
