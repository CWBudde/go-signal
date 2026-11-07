//go:build linux && (cgo || libsignal_go)

package accountimport

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	snapshotMountInfoBytes = 4 << 20
	mountEscapeWidth       = 4
)

type (
	sourceMount    struct{ device, root, point string }
	physicalRegion struct{ device, path string }
)

func mountTopologyError() error {
	return importError("unprovable mount topology", signal.ErrUnsupportedAccountImport)
}

func (snap *snapshot) readMounts(ctx context.Context) ([]sourceMount, error) {
	contextErr := ctx.Err()
	if contextErr != nil {
		return nil, importError("mount check canceled", contextErr)
	}

	raw, err := snap.mountInfoBytes()
	if err != nil {
		return nil, err
	}

	if len(raw) == 0 || len(raw) > snapshotMountInfoBytes {
		return nil, mountTopologyError()
	}

	var mounts []sourceMount

	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		contextErr = ctx.Err()
		if contextErr != nil {
			return nil, importError("mount check canceled", contextErr)
		}

		mount, parseErr := parseSourceMount(line)
		if parseErr != nil {
			return nil, parseErr
		}

		mounts = append(mounts, mount)
	}

	return mounts, nil
}

func (snap *snapshot) mountInfoBytes() ([]byte, error) {
	if snap.options.mountInfo != nil {
		raw, err := snap.options.mountInfo()
		if err != nil {
			return nil, mountTopologyError()
		}

		return raw, nil
	}

	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, mountTopologyError()
	}

	raw, err := io.ReadAll(io.LimitReader(file, snapshotMountInfoBytes+1))

	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return nil, mountTopologyError()
	}

	return raw, nil
}

func parseSourceMount(line string) (sourceMount, error) {
	fields := strings.Fields(line)

	separator := slices.Index(fields, "-")
	if len(fields) < 10 || separator < 6 || len(fields) < separator+4 {
		return sourceMount{}, mountTopologyError()
	}

	for _, value := range []string{fields[0], fields[1]} {
		_, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return sourceMount{}, mountTopologyError()
		}
	}

	if !validMountDevice(fields[2]) {
		return sourceMount{}, mountTopologyError()
	}

	root, err := sourceMountRoot(fields[3], fields[separator+1])
	if err != nil {
		return sourceMount{}, err
	}

	point, err := decodeMountPath(fields[4])
	if err != nil {
		return sourceMount{}, err
	}

	return sourceMount{device: fields[2], root: root, point: point}, nil
}

// Namespace mounts are singleton files. Linux prints their root as net:[inode]
// rather than a pathname; normalize that identity on its distinct nsfs device.
func sourceMountRoot(raw, filesystem string) (string, error) {
	if filesystem != "nsfs" || filepath.IsAbs(raw) {
		return decodeMountPath(raw)
	}

	kind, inode, found := strings.Cut(raw, ":[")
	if !found || kind == "" || strings.Trim(kind, "abcdefghijklmnopqrstuvwxyz") != "" ||
		!strings.HasSuffix(inode, "]") {
		return "", mountTopologyError()
	}

	_, err := strconv.ParseUint(strings.TrimSuffix(inode, "]"), 10, 64)
	if err != nil {
		return "", mountTopologyError()
	}

	return "/" + raw, nil
}

func validMountDevice(value string) bool {
	major, minor, found := strings.Cut(value, ":")
	if !found {
		return false
	}

	_, majorErr := strconv.ParseUint(major, 10, 32)
	_, minorErr := strconv.ParseUint(minor, 10, 32)

	return majorErr == nil && minorErr == nil
}

func decodeMountPath(raw string) (string, error) {
	escapes := map[string]byte{`\040`: ' ', `\011`: '\t', `\012`: '\n', `\134`: '\\'}

	var decoded []byte

	for offset := 0; offset < len(raw); offset++ {
		if raw[offset] != '\\' {
			decoded = append(decoded, raw[offset])
			continue
		}

		if len(raw)-offset < mountEscapeWidth {
			return "", mountTopologyError()
		}

		value, valid := escapes[raw[offset:offset+mountEscapeWidth]]
		if !valid {
			return "", mountTopologyError()
		}

		decoded = append(decoded, value)
		offset += mountEscapeWidth - 1
	}

	path := string(decoded)
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return "", mountTopologyError()
	}

	return path, nil
}

func pathInside(path, root string) bool {
	return path == root || root == "/" || strings.HasPrefix(path, root+"/")
}

// Filesystem coordinates reveal bind aliases without walking excluded source trees.
func physicalRegions(path string, mounts []sourceMount) ([]physicalRegion, error) {
	best, err := containingMount(path, mounts)
	if err != nil {
		return nil, err
	}

	relative := strings.TrimPrefix(strings.TrimPrefix(path, best.point), "/")

	regions := []physicalRegion{{device: best.device, path: filepath.Join(best.root, relative)}}
	for _, mount := range mounts {
		if pathInside(mount.point, path) {
			regions = append(regions, physicalRegion{device: mount.device, path: mount.root})
		}
	}

	slices.SortFunc(regions, func(left, right physicalRegion) int {
		if order := strings.Compare(left.device, right.device); order != 0 {
			return order
		}

		return strings.Compare(left.path, right.path)
	})

	return slices.Compact(regions), nil
}

func containingMount(path string, mounts []sourceMount) (sourceMount, error) {
	var best *sourceMount

	for index := range mounts {
		mount := &mounts[index]
		if !pathInside(path, mount.point) || (best != nil && len(mount.point) < len(best.point)) {
			continue
		}

		if best != nil && mount.point == best.point && (mount.device != best.device || mount.root != best.root) {
			return sourceMount{}, mountTopologyError()
		}

		best = mount
	}

	if best == nil {
		return sourceMount{}, mountTopologyError()
	}

	return *best, nil
}

func regionsOverlap(left, right []physicalRegion) bool {
	for _, first := range left {
		for _, second := range right {
			if first.device == second.device && pathsOverlap(first.path, second.path) {
				return true
			}
		}
	}

	return false
}

func stableRegions(previous *[]physicalRegion, current []physicalRegion) error {
	if *previous == nil {
		*previous = current
		return nil
	}

	if !slices.Equal(*previous, current) {
		return importError("mount bindings changed", signal.ErrAccountImportSourceChanged)
	}

	return nil
}

func (snap *snapshot) sourceDestinationRegions(mounts []sourceMount) ([]physicalRegion, []physicalRegion, error) {
	source, err := physicalRegions(snap.sourcePath, mounts)
	if err != nil {
		return nil, nil, err
	}

	destination, err := physicalRegions(snap.destination.resolved, mounts)
	if err != nil {
		return nil, nil, err
	}

	err = stableRegions(&snap.sourceRegions, source)
	if err != nil {
		return nil, nil, err
	}

	err = stableRegions(&snap.destinationRegions, destination)
	if err != nil {
		return nil, nil, err
	}

	if regionsOverlap(source, destination) {
		return nil, nil, importError("physical source/destination overlap", signal.ErrInvalidAccountImport)
	}

	return source, destination, nil
}

func (snap *snapshot) checkPhysicalContainment(ctx context.Context, workspace string) error {
	mounts, err := snap.readMounts(ctx)
	if err != nil {
		return err
	}

	source, destination, err := snap.sourceDestinationRegions(mounts)
	if err != nil {
		return err
	}

	if workspace == "" {
		return nil
	}

	temporary, err := physicalRegions(workspace, mounts)
	if err != nil {
		return err
	}

	if regionsOverlap(source, temporary) || regionsOverlap(destination, temporary) {
		return importError("physical workspace overlap", signal.ErrInvalidAccountImport)
	}

	return stableRegions(&snap.workspaceRegions, temporary)
}
