# Support many runner pod specs behind one listener with `runnerVariants`

**Status**: Proposed

## Context

Each `AutoscalingRunnerSet` (ARS) today registers exactly one runner scale set with
GitHub and manages exactly one runner pod spec. To run runners with different images or
resource requests (for example, a CPU pool and a GPU pool), an operator must create one
ARS per shape. Every extra ARS brings its own always-on listener pod plus its own
ServiceAccount, Role, RoleBinding, and listener config Secret.

On large deployments this cost is significant. One reported fleet runs about 88 ARS on a
single cluster (see issue #4169). The sets differ almost entirely along two axes: a small
number of scheduling shapes (resource requests) and the runner image. The result is about
88 listener pods and 88 RBAC bundles for what is, in effect, a handful of shapes crossed
with a set of images. The listener pods idle most of the time, but they still consume
scheduling slots, memory, and API watch connections, and each RBAC bundle adds objects the
control plane must track.

Splitting one pod spec per scale set is a GitHub Actions protocol requirement: the message
session that assigns jobs is keyed by scale set id, and a runner registers against one
scale set. The number of listener pods and RBAC bundles, however, is an ARC modeling
choice, not a protocol requirement. `github.com/actions/scaleset` (v0.4.0) already exposes
a per-scale-set-id, goroutine-safe `MessageSessionClient`; one client can hold many
sessions. So one listener process can drive several scale sets at once.

## Decision

Add an optional `runnerVariants` list to `AutoscalingRunnerSetSpec`. Each variant carries a
required DNS-label `name`, its own `runnerScaleSetLabels`, an optional pod `template`, and
optional `minRunners`/`maxRunners` that override the ARS-level values. When the list is
empty, the ARS behaves exactly as before.

When `runnerVariants` is set, one ARS:

- registers one runner scale set id per variant with GitHub, and records the
  `variantName -> id` map in a `runner-scale-set-ids` annotation (the scalar
  `runner-scale-set-id` annotation is still written for the default, so existing objects
  are untouched on upgrade);
- creates one `EphemeralRunnerSet` per variant (the default keeps the ARS name; a named
  variant is `<ars>-<variant>`, stamped with a `runner-variant` label);
- creates exactly one `AutoscalingListener`, and therefore one listener pod, one
  ServiceAccount, one Role, one RoleBinding, and one config Secret.

The per-variant scale set tuples (`name`, scale set id, ephemeral runner set name, min,
max) travel to the listener on an out-of-band `listener-scale-sets` annotation on the
`AutoscalingListener`, not inside its hashed spec. This is deliberate. The listener spec
and Role are hashed with `spew` to decide when to recreate the listener pod, and `spew`
prints every field, including nil slices and the type name. Adding any slice field to the
hashed spec would change the hash of every existing single-variant listener and force a
one-time pod recreation across the whole fleet on upgrade. Keeping the tuples on an
annotation that is absent for single-variant sets keeps the listener spec, Role, and
`config.json` byte-for-byte identical for the unchanged path.

The listener process (`cmd/ghalistener`) gains an optional `scaleSets` list in its config.
When present, `run()` starts one supervised session per scale set, each with its own
message session client, listener loop, scaler, and a per-scale-set metric label. A
transient error in one session does not cancel its siblings; each session is retried under
capped backoff, and only context cancellation or an unrecoverable auth or config error is
fatal. An empty `scaleSets` list keeps the original single-session behavior.

Back-compat is anchored by an `EffectiveVariants()` resolver that returns one synthetic
variant with an empty name (carrying today's scalar values) when the list is empty. Every
builder and reconciler loops over this resolver, and the empty-name element reproduces
today's names, labels, and hashes. Golden-hash tests captured from the release tip pin the
ephemeral runner set integrity hash, the listener spec hash, the Role integrity hash, and
the runner-set spec hash for the empty-variant path.

## Consequences

Operators can collapse a family of same-shape, different-image (or different-resource) scale
sets into one ARS. That removes one listener pod and one RBAC bundle per collapsed set,
which is the main saving on large clusters. Runner scale set ids, ephemeral runner sets, and
runner pods stay one-per-variant, because those are fixed by the protocol and are cheap or
on-demand.

The ARS-level `Hash()` and `ListenerSpecHash()` change once, because the spec gains a field.
That produces a single Pending-to-Running flip on the ARS on upgrade, with no child object
churn. No existing single-variant deployment changes its listener pod, RBAC, or
`config.json`.

One listener process now holds several sessions, so a bug in the fan-out or supervision
logic could affect more than one variant at once. The failure-isolation design (per-session
supervision, no sibling cancellation) limits the blast radius, and the single-session path
is unchanged for anyone who does not use variants.

The per-variant `template` is a verbatim pass-through in the Helm chart, not assembled from
`containerMode`. A variant that needs dind or kubernetes mode must spell out its own pod
spec. This keeps the variant surface small and explicit at the cost of some repetition
between the top-level `template` and a variant `template`.
