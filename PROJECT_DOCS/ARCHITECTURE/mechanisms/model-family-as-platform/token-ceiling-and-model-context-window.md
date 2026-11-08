#### Token ceiling and model context window

The default `context.token-budget` (200000, SPEC §2.6.1) is the token ceiling composition enforces.
256K is the model context window the simulation and working-set sizing assume, so the ceiling sits
below it. The window figure affects active-threads slot byte budgets, the pressure to close threads,
recall query depth, and the mock client's need to simulate token counts for budget-exhaustion
testing.

