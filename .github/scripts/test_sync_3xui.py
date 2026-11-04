"""Local, credential-free regression tests for the 3X-UI update workflow."""
import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import sync_3xui as sync


class UpstreamSyncTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.cwd = Path.cwd()
        self.addCleanup(os.chdir, self.cwd)
        root = Path(self.tmp.name)
        self.origin, self.work, self.upstream = (root / x for x in ("origin", "work", "upstream"))
        self.git("init", "--bare", str(self.origin))
        self.git("-C", str(self.origin), "symbolic-ref", "HEAD", "refs/heads/main")
        self.git("init", "-b", "main", str(self.work))
        os.chdir(self.work)
        self.git("config", "user.name", "Test")
        self.git("config", "user.email", "test@example.invalid")
        Path("shared.txt").write_text("base\n")
        self.commit("base")
        self.git("remote", "add", "origin", str(self.origin))
        self.git("push", "origin", "main")
        self.git("clone", str(self.origin), str(self.upstream))
        self.git("-C", str(self.upstream), "config", "user.name", "Test")
        self.git("-C", str(self.upstream), "config", "user.email", "test@example.invalid")
        Path("fork.txt").write_text("ElX extensions\n")
        self.commit("fork feature")
        self.git("push", "origin", "main")
        self.main_sha = self.git("rev-parse", "main").stdout.strip()
        self.calls = []

    def git(self, *args):
        return subprocess.run(("git", *args), check=True, capture_output=True, text=True)

    def commit(self, message):
        self.git("add", ".")
        self.git("commit", "-m", message)

    def release(self, conflict=False, changed=True):
        if conflict:
            Path("shared.txt").write_text("fork\n")
            self.commit("fork modifies shared path")
            self.git("push", "origin", "main")
            self.main_sha = self.git("rev-parse", "main").stdout.strip()
        if changed:
            (self.upstream / ("shared.txt" if conflict else "upstream.txt")).write_text("upstream\n")
            self.git("-C", str(self.upstream), "add", ".")
            self.git("-C", str(self.upstream), "commit", "-m", "upstream update")
        self.git("-C", str(self.upstream), "tag", "v1")

    def execute(self, pending=None, tag="v1"):
        original = sync.command
        def fake(*args, check=True):
            if args[0] == "git":
                if args[1] == "fetch":
                    args = (*args[:3], str(self.upstream), *args[4:])
                return original(*args, check=check)
            self.calls.append(args)
            if args[1:3] == ("pr", "list"):
                value = json.dumps(pending or [])
            elif args[1:3] == ("pr", "create"):
                value = "https://github.com/example/fork/pull/1"
            elif args[1] == "api" and args[2].endswith("/releases/latest"):
                value = json.dumps({"tag_name": tag})
            else:
                raise AssertionError(args)
            return subprocess.CompletedProcess(args, 0, value, "")
        with patch.object(sync, "command", side_effect=fake), contextlib.redirect_stdout(io.StringIO()):
            sync.prepare("example/fork")
        self.assertEqual(self.git("rev-parse", "main").stdout.strip(), self.main_sha)
        self.assertEqual(self.git("--git-dir", str(self.origin), "rev-parse", "main").stdout.strip(), self.main_sha)

    def test_clean_release_creates_draft_and_preserves_fork(self):
        self.release()
        self.execute()
        self.assertEqual(Path("fork.txt").read_text(), "ElX extensions\n")
        self.assertEqual(Path("upstream.txt").read_text(), "upstream\n")
        self.assertTrue(any("--draft" in call for call in self.calls))
        self.assertFalse(any("merge" in call for call in self.calls if call[0] == "gh"))

    def test_conflicts_recorded_without_publishing_partial_merge(self):
        self.release(conflict=True)
        self.execute()
        marker = json.loads(Path(".github/upstream-conflict.json").read_text())
        self.assertIn("shared.txt", marker["conflicts"])
        self.assertEqual(Path("shared.txt").read_text(), "fork\n")
        self.assertTrue(any("--draft" in call for call in self.calls))

    def test_already_present_release_skips(self):
        self.release(changed=False)
        self.execute()
        self.assertFalse(any(call[1:3] == ("pr", "create") for call in self.calls))

    def test_duplicate_open_pr_skips(self):
        self.execute(pending=[{"headRefName": "updates/3x-ui-existing"}])
        self.assertFalse(any(call[1:3] == ("pr", "create") for call in self.calls))

    def test_malicious_tag_rejected(self):
        with self.assertRaises(ValueError):
            self.execute(tag="../bad")


if __name__ == "__main__":
    unittest.main()
