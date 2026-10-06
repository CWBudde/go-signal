"""Install test-only scheduling barriers in a disposable fork source copy."""

import pathlib
import sys

path = pathlib.Path(sys.argv[1])
source = path.read_text()
anchors = {
    "\t\t\t\trequest.RequestMessage.Id = &i":
        "\t\t\t\tlifecycleBeforeResponseRegistrationForTest(responseChannels, request.ResponseChannel)\n",
    "\t\twg.Wait()": "\t\tlifecycleBeforeWorkerJoinForTest()\n",
}
for anchor, barrier in anchors.items():
    if source.count(anchor) != 1:
        sys.exit(f"expected exactly one lifecycle barrier anchor: {anchor!r}")
    source = source.replace(anchor, barrier + anchor)
path.write_text(source)
