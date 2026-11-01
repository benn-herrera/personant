"""Golden tests for dicer.py.

Each case under testdata/ runs the CLI in a scratch copy of its input/ with the arguments in
`args` (one per line) and the environment in `env` (KEY=VALUE lines; DICER_KEEP is otherwise
unset), and must reproduce expected/: every file of expected/tree/, and stdout, stderr and the
exit status byte for byte. The expected outputs were produced by the shell/awk implementation
this port replaced (its plumbing files and empty directories left out).
"""

import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from typing import Optional
from pathlib import Path

from dicerlib.refactor import check_hunks

HERE = Path(__file__).resolve().parent
TESTDATA = HERE / "testdata"
CLI = HERE / "dicer.py"


def _files(root: Path) -> dict[str, bytes]:
    return {p.relative_to(root).as_posix(): p.read_bytes() for p in sorted(root.rglob("*")) if p.is_file()}


def _case_env(case: Path) -> dict[str, str]:
    env = {k: v for k, v in os.environ.items() if k != "DICER_KEEP"}
    env_file = case / "env"
    if env_file.exists():
        for line in env_file.read_text(encoding="utf-8").splitlines():
            key, _, value = line.partition("=")
            env[key] = value
    return env


def _scratch_root() -> Optional[str]:
    """Where case scratch directories go: $DICER_SCRATCH, created if absent, else the system temp."""
    root = os.environ.get("DICER_SCRATCH")
    if root:
        os.makedirs(root, exist_ok=True)
    return root or None


class GoldenCases(unittest.TestCase):
    def test_cases(self) -> None:
        cases = sorted(p for p in TESTDATA.iterdir() if p.is_dir())
        self.assertTrue(cases, "no cases under testdata/")
        for case in cases:
            with self.subTest(case=case.name), tempfile.TemporaryDirectory(dir=_scratch_root()) as scratch:
                run = Path(scratch) / "run"
                shutil.copytree(case / "input", run)
                args = (case / "args").read_text(encoding="utf-8").splitlines()
                proc = subprocess.run([sys.executable, str(CLI), *args], cwd=run, env=_case_env(case), capture_output=True)
                expected = case / "expected"
                self.assertEqual(proc.stdout, (expected / "stdout").read_bytes())
                self.assertEqual(proc.stderr, (expected / "stderr").read_bytes())
                self.assertEqual(f"{proc.returncode}\n", (expected / "exit").read_text(encoding="utf-8"))
                self.assertEqual(_files(run), _files(expected / "tree"))


class HunkCheck(unittest.TestCase):
    def test_edit_beyond_a_conversion_is_bad(self) -> None:
        old = "## S\n\n**T.** body\n\nplain\n"
        new = "## S\n\n### T\n\nbody, edited\n\nplain\n"
        self.assertEqual(check_hunks(old, new), (1, ["BAD merged hunk at old line 3: **T.** body"]))


if __name__ == "__main__":
    unittest.main()
