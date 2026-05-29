# Fixture: clean-rename-go

**Category:** (none — clean diff) · **Language:** Go

A pure local-variable rename (`cnt` → `count`) with no behavioral change.
A good review must stay **silent**: there are no expected findings, and the
two renamed lines are `must_stay_silent_on` anchors. This fixture measures
the false-positive axis — an over-eager provider that "finds" something
here is producing noise.

**Provenance:** hand-authored clean control (ADR-0018 §1). Every corpus
needs at least one zero-finding diff to catch over-flagging.
