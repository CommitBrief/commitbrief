# Fixture: yaml-unsafe-load-python
**Category:** security · **Language:** Python

Config import switches from `yaml.safe_load` to `yaml.load(..., Loader=yaml.Loader)` to support custom tags. The full loader constructs arbitrary Python objects from uploaded YAML, which allows code execution.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
