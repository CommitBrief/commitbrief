# Fixture: laravel-n-plus-one-php
**Category:** performance · **Language:** PHP

A Laravel controller lists posts and the resource reads `$post->author->name` per row. The query has no `with('author')`, so every post triggers an additional author query.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
