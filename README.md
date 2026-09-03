# Throwaway prototype — live-agent TUI layout and information architecture

Disposable evidence for wayfinder ticket
[#450](https://github.com/constructorfleet/forge/issues/450). **Not code to keep.** The output of
this prototype is the decided information architecture, recorded on that ticket. No Engine wiring,
no store access, fake data only.

## Run

    go run . -dump running -twoline     # the decided frame
    go run . -dump <scenario> [-twoline]
    go run . -bench                     # synthetic high-rate load over the ring buffer

Scenarios: `running`, `expanded`, `paused`, `reattach`, `wedged`, `planning`, `empty`, `dead`.

## Files

| File | Role |
|---|---|
| `ring.go` | The capped ring buffer #445 said the recommended path makes you own. 40 lines. |
| `events.go` | One event to display lines, per event type. |
| `frame.go` | Headless frame renderer — a pure `frame` to `string`, no framework. |
| `main.go` | Scenario fixtures and the load bench. |

`frame.go` deliberately has **no Bubble Tea import**. The frame is a pure function of a plain
struct, which is the testability property that decided the framework bake-off — the same shape
works under `teatest`, and the fixtures here are terminal-free.

## Measured

    10000 events  ingest   34ms  3.38 µs/event  held 2000  evicted 31408    frame  21µs
   100000 events  ingest  124ms  1.24 µs/event  held 2000  evicted 331254   frame  20µs
  1000000 events  ingest 1.259s  1.26 µs/event  held 2000  evicted 3332990  frame  20µs

Ingest is O(1) per line and includes rendering the event to lines. Memory is constant at any run
length. Frame materialization is the only O(window) operation and runs once per frame, never per
event.
