## Mental model summary

> The service is the holder of authoritative state and the single writer. The model is a narrow
> judgment instrument. opencode owns everything except what enters the request. Every
> working-window mutation goes through the per-delta pipeline (§3.0). Every dependency earns its
> keep with a real consumer. Testing is the measurement instrument, not a quality check. Constraints
> are load-bearing.
