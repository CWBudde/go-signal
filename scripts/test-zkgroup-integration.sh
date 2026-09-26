#!/usr/bin/env bash
# Exercise signalmeow's private receive paths without adding changes outside the
# fork's pkg/libsignalgo package. The overlay exists only for this test invocation.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
module_dir=$(go list -f '{{.Dir}}' go.mau.fi/mautrix-signal/pkg/signalmeow)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
python3 - "$module_dir" "$root" "$test_dir" <<'PY'
import json, pathlib, sys
module_dir, root, test_dir = map(pathlib.Path, sys.argv[1:])
overlay = {"Replace": {str(module_dir / "zkgroup_integration_test.go"):
                       str(root / "scripts/testdata/zkgroup_integration_test.go")}}
helper = module_dir.parent / "libsignalgo/groupsendendorsement_test.go"
(test_dir / "group_send_helpers_test.go").write_text(helper.read_text().replace("package libsignalgo_test", "package signalmeow", 1))
overlay["Replace"][str(module_dir / "group_send_helpers_test.go")] = str(test_dir / "group_send_helpers_test.go")
overlay["Replace"][str(module_dir / "group_send_integration_test.go")] = str(root / "scripts/testdata/group_send_integration_test.go")
(test_dir / "overlay.json").write_text(json.dumps(overlay))
PY
GROUP_SEND_TEST_FIXTURE="$module_dir/../libsignalgo/testdata/group-send.json" go test -overlay "$test_dir/overlay.json" "$@" -count=1 -run '^TestZKGroupIntegration' go.mau.fi/mautrix-signal/pkg/signalmeow
