"""Offline regressions for the metadata used by nightly prebuilds."""
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location(
    "matrix", ROOT / "scripts/gen-prebuild-matrix.py"
)
matrix = importlib.util.module_from_spec(spec)
spec.loader.exec_module(matrix)


class PackagistVersionsTest(unittest.TestCase):
    def setUp(self):
        self.data = json.loads(
            (ROOT / "internal/resolver/testdata/packagist-minified.json").read_text()
        )

    def versions(self, keep=10):
        with patch.object(matrix, "http_json", return_value=self.data):
            return matrix.packagist_versions("xdebug/xdebug", keep)

    def test_stable_releases_inherit_metadata_from_prerelease(self):
        self.assertEqual(self.versions(), [
            ("3.5.3", ">=8.0,<8.6"),
            ("3.5.2", ">=8.0,<8.6"),
        ])
        self.assertEqual(self.versions(1), [("3.5.3", ">=8.0,<8.6")])

    def test_expansion_replaces_nested_objects_and_keeps_snapshots(self):
        releases = self.data["packages"]["xdebug/xdebug"]
        expanded = matrix.expand_versions(releases)
        self.assertIn("lib-zlib", expanded[0]["require"])
        self.assertEqual(expanded[1]["require"], {"php": ">=8.0,<8.6"})
        self.assertNotIn("shasum", expanded[1]["dist"])
        self.assertEqual(expanded[4], {"version": "3.5.0"})
        self.assertNotIn("type", releases[1])  # input remains minified

    def test_unminified_releases_do_not_inherit_fields(self):
        del self.data["minified"]
        with self.assertRaisesRegex(SystemExit, "no stable release declares"):
            self.versions()

    def test_each_release_must_have_extension_type(self):
        releases = self.data["packages"]["xdebug/xdebug"]
        releases.insert(1, {
            "version": "3.6.0", "type": "library", "php-ext": {"priority": 90}
        })
        releases[2]["type"] = "php-ext-zend"
        self.assertEqual(self.versions(1), [("3.5.3", ">=8.0,<8.6")])

    def test_unset_php_ext_is_not_inherited_by_older_releases(self):
        releases = self.data["packages"]["xdebug/xdebug"]
        del releases[3]["type"]
        self.assertEqual(len(self.versions()), 2)


if __name__ == "__main__":
    unittest.main()
