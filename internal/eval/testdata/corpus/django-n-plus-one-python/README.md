# Fixture: django-n-plus-one-python
**Category:** performance · **Language:** Python

A Django view renders an order list and reads `order.customer.name` inside the loop. The queryset has no `select_related("customer")`, so every row triggers an extra query (classic ORM N+1).

**Provenance:** hand-authored planted defect (ADR-0018 §1).
