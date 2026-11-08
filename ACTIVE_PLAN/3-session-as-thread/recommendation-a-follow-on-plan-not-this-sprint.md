### Recommendation: a follow-on plan, not this sprint

1. **It is a behaviour redesign, not a removal.** It rewrites SPEC §1.1, §3.0.4, §3.2, §3.3, §5.1,
   §5.5 and §9.1, adds a stored field with a home-format bump, and rebuilds the sim generator's
   unit of work. WP6 and WP7 already shift the mock gate's behaviour. Two behaviour changes in one
   sprint would leave no way to tell which one moved a metric.
2. **Its input arrives with A5.** Session IDs come from the plugin. Before A5, the in-process sim
   would have to invent sessions, without the real source to check it against.
3. **Nothing here depends on it, and it depends on WP7.** Autonomic recall is how a new session
   finds an old topic.

This sprint records the outcome in ROADMAP only (WP10). The SPEC moves with the follow-on's code.
The follow-on plan sits after this strip and alongside or after A3, and replaces A10.

