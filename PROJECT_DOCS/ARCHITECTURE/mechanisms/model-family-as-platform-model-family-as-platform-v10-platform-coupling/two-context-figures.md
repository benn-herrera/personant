#### Two context figures

The default `context.token-budget` (200000, SPEC §2.6.1) is the ceiling
composition enforces. 256K is the model window the sim and working-set sizing assume, so the
ceiling sits below it. The window figure affects Layer B byte budgets, closure pressure, recall
query depth, and the mock-client's need to simulate token counts for budget-exhaustion testing.

