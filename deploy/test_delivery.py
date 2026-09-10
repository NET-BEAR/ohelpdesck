"""Behavioral tests for the trusted deployment boundary; no real SSH or Docker calls."""
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

SOURCE = Path(__file__).parent
SHA = "a" * 40
OLD = "b" * 40


def archive(content=b"echo untrusted-must-not-run", name="deploy/remote-apply.sh"):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as tar:
        item = tarfile.TarInfo(name)
        item.size = len(content)
        tar.addfile(item, io.BytesIO(content))
    return output.getvalue()


class DeliveryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        for directory in ("bin", "config", "shared", "releases", "fake"):
            (self.root / directory).mkdir()
        self.log = self.root / "calls"
        self.env = dict(os.environ, CALLS=str(self.log), PATH=f"{self.root / 'fake'}:{os.environ['PATH']}")
        self.write_script("fake/docker", 'echo "docker $*" >> "$CALLS"\nif [ "${FAIL_UP:-}" = yes ] && [ "$*" != "" ]; then case "$*" in *"up -d"*) exit 1;; esac; fi\n')
        self.write_script("fake/curl", 'echo "curl $*" >> "$CALLS"\n')
        for filename in ("receive-deploy", "remote-apply"):
            source = (SOURCE / f"{filename}.sh").read_text()
            self.write_script(f"bin/{filename}", source.replace("/opt/ohelpdesck-dev", str(self.root)))

    def tearDown(self):
        self.temp.cleanup()

    def write_script(self, name, content):
        target = self.root / name
        target.write_text("#!/bin/bash\n" + content)
        target.chmod(0o700)

    def receive(self, body, command=f"deploy {SHA}"):
        return subprocess.run(["bash", str(self.root / "bin/receive-deploy")], input=body,
                              env=dict(self.env, SSH_ORIGINAL_COMMAND=command), capture_output=True)

    def calls(self):
        return self.log.read_text() if self.log.exists() else ""

    def test_rejects_other_commands(self):
        self.assertEqual(self.receive(b"", "id").returncode, 64)
        self.assertEqual(self.calls(), "")

    def test_rejects_traversal_and_cleans_staging(self):
        self.assertNotEqual(self.receive(archive(name="../../escape")).returncode, 0)
        self.assertFalse((self.root / "releases" / SHA).exists())
        self.assertEqual(list((self.root / "releases").iterdir()), [])

    def test_invalid_archive_can_be_retried(self):
        self.assertNotEqual(self.receive(b"broken").returncode, 0)
        self.assertEqual(self.receive(archive()).returncode, 0)

    def test_uses_only_trusted_script_and_manifest(self):
        result = self.receive(archive(content=b"exit 97"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(str(self.root / "config/compose.yml"), self.calls())
        self.assertNotIn(f"releases/{SHA}/deploy/compose.yml", self.calls())

    def test_current_replay_does_not_rebuild(self):
        body = archive()
        self.assertEqual(self.receive(body).returncode, 0)
        self.log.write_text("")
        self.assertEqual(self.receive(body).returncode, 0)
        self.assertNotIn(" build ", self.calls())
        self.assertNotIn(" stop ", self.calls())
        self.assertIn("--no-build", self.calls())

    def test_same_sha_different_source_rejected(self):
        self.assertEqual(self.receive(archive()).returncode, 0)
        self.assertEqual(self.receive(archive(b"changed")).returncode, 65)

    def test_failed_first_release_stops_apps_only(self):
        self.env["FAIL_UP"] = "yes"
        # Fail application switch, not dependency startup.
        self.write_script("fake/docker", 'echo "docker $*" >> "$CALLS"\ncase "$*" in *"--no-build --wait"*) exit 1;; esac\n')
        result = self.receive(archive())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("stop api worker web", self.calls())
        self.assertNotIn(" down ", self.calls())
        self.assertFalse((self.root / "current").exists())

    def test_failed_upgrade_preserves_current(self):
        old_path = self.root / "releases" / OLD
        old_path.mkdir()
        (self.root / "current").symlink_to(old_path)
        self.write_script("fake/docker", f'echo "docker $* release=$RELEASE_SHA" >> "$CALLS"\nif [ "$RELEASE_SHA" = {SHA} ]; then case "$*" in *"--no-build --wait"*) exit 1;; esac; fi\n')
        self.assertNotEqual(self.receive(archive()).returncode, 0)
        self.assertIn(f"release={OLD}", self.calls())
        self.assertEqual((self.root / "current").resolve(), old_path)
        self.assertNotIn("stop api", self.calls())


if __name__ == "__main__":
    unittest.main(verbosity=2)
