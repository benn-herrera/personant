#!/usr/bin/env python3
"""Mad-libs recall-fidelity query generator (Phase C.2).

Reads the hand-crafted topic templates under ``templates/`` and emits a
deterministic, seeded set of fill-in-the-blank queries to
``queries.json``.

The asymmetry the design rests on: a thread's stored substrate is
lexically *fixed* (its anchor set), while a retrieval query is lexically
*drifty* (the synthetic user re-encodes the topic). A template models
one topic cluster — its ``anchors`` are the stored side, its
``columns`` x ``sentence_templates`` span the query side.

Phase C.2 is the end-to-end harness validation: every column cell is
itself an anchor, so each generated query reliably clears the §3.4
Jaccard threshold for its source topic. Genuine vocabulary drift —
synonym/phrase cells that do *not* overlap the anchor set, the case
that stresses Jaccard's hard floor — is introduced by the C.3
adversarial templates.

Output is a DERIVED artifact: ``.gitignore``-d, regenerated via
``make recall-madlibs``. Canonical sources are this script and the
template JSON files.

Stdlib only (AGENTS.md auxiliary-script constraint). Determinism: a
seeded ``random.Random`` (Mersenne Twister, stable across CPython
releases) shuffles the enumerated query space; the same seed always
yields the same ``queries.json`` byte-for-byte.
"""

import argparse
import json
import pathlib
import random
import re
import sys

SCRIPT_DIR = pathlib.Path(__file__).resolve().parent
DEFAULT_TEMPLATES_DIR = SCRIPT_DIR / "templates"
DEFAULT_OUT = SCRIPT_DIR / "queries.json"
DEFAULT_SEED = 20260515
DEFAULT_SAMPLES = 10

PLACEHOLDER_RE = re.compile(r"\{(\d+)\}")


VALID_MODES = ("strict", "measure-only")


def load_template(path):
    """Load and validate one template JSON file.

    Optional fields:
      - ``topic``: the ground-truth topic key (defaults to ``name``).
        Multiple templates may share a topic — a clean template and
        its adversarial variants — and the harness seeds one thread
        per distinct topic.
      - ``mode``: ``strict`` (default) or ``measure-only``. Strict
        templates must overlap their topic (every cell is an anchor);
        measure-only templates may carry non-overlapping drift cells.
    """
    with path.open(encoding="utf-8") as fh:
        tpl = json.load(fh)

    for key in ("name", "anchors", "columns", "sentence_templates"):
        if key not in tpl:
            raise ValueError(f"{path.name}: missing required key {key!r}")

    tpl.setdefault("topic", tpl["name"])
    tpl.setdefault("mode", "strict")
    if tpl["mode"] not in VALID_MODES:
        raise ValueError(
            f"{path.name}: mode {tpl['mode']!r} not in {VALID_MODES}"
        )

    anchors = set(tpl["anchors"])
    if not 4 <= len(tpl["anchors"]) <= 8:
        raise ValueError(
            f"{path.name}: anchor count {len(tpl['anchors'])} outside the "
            f"spec §2.2 range of 4-8"
        )

    # Strict templates overlap by construction: a query built from one
    # cell per column always intersects the source thread's anchors.
    # Measure-only (adversarial) templates intentionally drift, so the
    # check is relaxed for them — the non-overlap IS the measurement.
    if tpl["mode"] == "strict":
        for col_idx, column in enumerate(tpl["columns"]):
            for cell in column:
                if cell not in anchors:
                    raise ValueError(
                        f"{path.name}: column {col_idx} cell {cell!r} is not "
                        f"in the anchor set; a strict template must overlap "
                        f"its topic (use mode=measure-only for drift cells)"
                    )

    n_cols = len(tpl["columns"])
    for sent in tpl["sentence_templates"]:
        slots = {int(m) for m in PLACEHOLDER_RE.findall(sent)}
        if slots != set(range(n_cols)):
            raise ValueError(
                f"{path.name}: sentence template {sent!r} uses slots {sorted(slots)}, "
                f"expected exactly {{0..{n_cols - 1}}}"
            )
    return tpl


def enumerate_queries(tpl):
    """Return every (sentence_index, cell-tuple) pair for a template.

    The cell tuple has one entry per column. Sorted so enumeration is
    order-stable before the seeded shuffle.
    """
    columns = tpl["columns"]
    combos = [()]
    for column in columns:
        combos = [combo + (cell,) for combo in combos for cell in column]
    pairs = []
    for sent_idx in range(len(tpl["sentence_templates"])):
        for combo in combos:
            pairs.append((sent_idx, combo))
    pairs.sort()
    return pairs


def render(tpl, sent_idx, cells):
    """Render one query string: sentence template with #tag cells."""
    sentence = tpl["sentence_templates"][sent_idx]
    return PLACEHOLDER_RE.sub(lambda m: "#" + cells[int(m.group(1))], sentence)


def collect_topics(templates):
    """Return the ordered distinct topics across all templates.

    Templates sharing a topic must declare identical anchor sets — the
    anchors are the lexically-fixed stored substrate of the seeded
    thread, and a topic seeds exactly one thread.
    """
    topics = []
    seen = {}
    for tpl in templates:
        name = tpl["topic"]
        if name in seen:
            if seen[name] != tpl["anchors"]:
                raise ValueError(
                    f"templates for topic {name!r} declare conflicting "
                    f"anchor sets"
                )
            continue
        seen[name] = tpl["anchors"]
        topics.append({"name": name, "anchors": tpl["anchors"]})
    return topics


def build(templates, seed, samples):
    """Build the queries.json document body."""
    out_queries = []
    for tpl in templates:
        pairs = enumerate_queries(tpl)
        # Per-template seeded RNG: adding a template does not perturb the
        # query stream of templates authored before it.
        rng = random.Random(f"{seed}:{tpl['name']}")
        rng.shuffle(pairs)
        chosen = pairs[: min(samples, len(pairs))]

        for i, (sent_idx, cells) in enumerate(chosen):
            out_queries.append(
                {
                    "id": f"{tpl['name']}-{i:02d}",
                    "template": tpl["name"],
                    "topic": tpl["topic"],
                    "mode": tpl["mode"],
                    "tags": list(cells),
                    "user_input": render(tpl, sent_idx, cells),
                }
            )

    return {
        "_comment": "DERIVED ARTIFACT - generated by generate.py; do not edit; "
        "not committed (see .gitignore). Regenerate with `make recall-madlibs`.",
        "seed": seed,
        "samples_per_template": samples,
        "topics": collect_topics(templates),
        "queries": out_queries,
    }


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--seed", type=int, default=DEFAULT_SEED)
    parser.add_argument(
        "--samples",
        type=int,
        default=DEFAULT_SAMPLES,
        help="queries generated per template",
    )
    parser.add_argument("--templates-dir", type=pathlib.Path, default=DEFAULT_TEMPLATES_DIR)
    parser.add_argument("--out", type=pathlib.Path, default=DEFAULT_OUT)
    args = parser.parse_args(argv)

    template_paths = sorted(args.templates_dir.glob("*.json"))
    if not template_paths:
        print(f"no templates found under {args.templates_dir}", file=sys.stderr)
        return 1

    templates = [load_template(p) for p in template_paths]
    doc = build(templates, args.seed, args.samples)

    # Stable formatting: a re-run with the same inputs is byte-identical.
    text = json.dumps(doc, indent=2, sort_keys=True) + "\n"
    args.out.write_text(text, encoding="utf-8")
    print(
        f"wrote {args.out} - {len(doc['queries'])} queries "
        f"across {len(templates)} templates"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
