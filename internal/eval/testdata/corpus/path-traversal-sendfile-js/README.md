# Fixture: path-traversal-sendfile-js

**Category:** security · **Language:** JavaScript

A download route joins the `:name` route parameter onto the uploads directory and calls
`res.sendFile`. Express decodes `%2e%2e%2f`, so `name = "../../etc/passwd"` escapes the directory. The
absolute-path form is required by `sendFile`, which is why the author reached for `path.join` without
checking containment.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 6 lines.
