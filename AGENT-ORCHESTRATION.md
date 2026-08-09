# Agent orchestration - Plus / GPT-5.6

## Recommended model allocation

Do not keep GPT-5.6 Sol as a long-lived supervisor merely to spawn implementation subagents.

Use:
- **GPT-5.6 Luna + max** as the default implementation agent for each phase.
- **GPT-5.6 Sol + high** for narrow architecture/security/concurrency reviews at high-risk gates, not for routine file editing.
- Avoid `ultra` for normal implementation; use it only when a task genuinely splits into several independent workstreams and wall-clock time matters more than token/credit use.

Recommended high-risk Sol `high` reviews:
1. after Phase B: public-link protocol + crypto/integrity assumptions;
2. after Phase E: scheduler/checkpoint/race/resource correctness;
3. after Phase H: release/security audit.

If Luna reaches the same concrete blocker twice after inspecting evidence and tests, escalate that blocker to Sol instead of restarting broad implementation with Sol.

After a Sol `high` review, if the finding requires non-trivial implementation changes, return the same phase to a fresh Luna `max` task, rerun the phase gate, then repeat the Sol `high` review. Sol is the reviewer/escalation path, not the default implementation owner.

## Parallelism

Default to one Luna max task at a time, one phase at a time.

Use two concurrent Luna max tasks only after boundaries are frozen, for example:
- backend API implementation and frontend consumption with a fixed contract;
- implementation and independent read-only test/security review.

Do not parallelize code that shares mutable state or the same package. Integration cost usually dominates any speed gain.

## Canonical phase implementation prompt

This is the **only** implementation prompt template. Do not maintain a shorter or phase-specific variant elsewhere. For a phase task, replace every `<PHASE>` token with exactly one phase name from `PLAN.md` (for the first task: `Phase A`) and send the resulting text verbatim to **GPT-5.6 Luna, reasoning `max`**.

```text
You are the implementation owner for <PHASE> of this repository.

Read `AGENTS.md` and the complete `PLAN.md` before editing. Read `PLAN-UPLOAD.md` and `PLAN-STREAMING.md` only to avoid incompatible decisions; they are deferred scope and must not be implemented.

Work autonomously and non-interactively until the complete <PHASE> gate passes. Do not give me an upfront plan, preamble, progress narration, or ask for confirmation for ordinary implementation decisions.

Inspect the existing repository before editing. Keep changes strictly within <PHASE>. Implement the entire phase, add or repair all required tests, run focused tests while iterating, then run every gate required for <PHASE>. Persist until the gate passes.

Do not change architecture, dependency choices, security rules, resource caps, or tests merely to make progress. Never weaken or delete a failing test unless the specification itself is demonstrably wrong. If an upstream dependency or MEGA behavior makes the phase impossible, prove it with a minimal reproduction and record the exact blocker, evidence, attempted fixes, and smallest proposed replacement in `BLOCKERS.md`; otherwise continue.

Use subagents only for genuinely independent work with disjoint write ownership. Default to no subagents. Never have two agents edit the same package, schema, contract, or shared mutable subsystem concurrently. You own integration and must rerun the full phase gate after any subagent work.

Do not rewrite `PLAN.md`, `PLAN-UPLOAD.md`, or `PLAN-STREAMING.md`. Do not implement later phases opportunistically.

When finished, report only:
1. files or surfaces materially changed;
2. exact gate commands and pass/fail results;
3. resource measurements if this phase requires them;
4. concrete remaining blockers or risks.

Do not include generic summaries or future-work suggestions.
```

### First launch

For the first task, use the canonical prompt above with `<PHASE>` replaced by `Phase A`. Do not use a separate Phase A prompt.

## Sol high review prompt

Review the completed `<PHASE>` implementation as a release-blocking senior reviewer.

Read `AGENTS.md`, the relevant `PLAN.md` phase and gate, then inspect only the implementation/diff and tests needed to verify that contract. Prioritize correctness, cryptography/protocol assumptions, crash durability, concurrency/races, security boundaries, resource bounds, and hidden scope expansion.

Do not refactor for style and do not add features. Reproduce suspected defects with tests or commands where feasible. Fix only clear defects that are local and low-risk; otherwise produce a short blocking finding with file/function, failure mode, reproduction, and required correction.

Run the relevant gate after any fix. Return findings ordered by severity, followed by exact test/gate results. If no blocking defect is found, say so explicitly and list only residual risks supported by evidence.
