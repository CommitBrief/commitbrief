# Fixture: django-get-does-not-exist-python
**Category:** error-handling · **Language:** Python

A new endpoint looks up an object with `Model.objects.get(pk=...)` from a URL parameter and does not handle `DoesNotExist`, so an unknown id surfaces as a 500 instead of a 404.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
