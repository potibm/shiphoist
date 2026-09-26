package engine

import (
	"fmt"

	"github.com/potibm/shiphoist/internal/core"
)

// fetchGroup is a set of discovered updates that resolve to the same registry
// answer, so only one of them needs to be fetched.
type fetchGroup struct {
	// Representative is the update actually sent to the registry.
	Representative core.ImageUpdate

	// Members are every update sharing the representative's key, including
	// the representative itself. Each keeps its own declaration site.
	Members []core.ImageUpdate
}

// key identifies a group. Two references resolve identically only when image,
// tag and existing digest all match.
func (g fetchGroup) key() string {
	return fetchKey(g.Representative)
}

// size is the number of references the group stands for.
func (g fetchGroup) size() int {
	return len(g.Members)
}

// label names the group for progress output. A shared fetch belongs to no
// single service, so the service prefix is only meaningful when the group has
// exactly one member.
func (g fetchGroup) label() string {
	if len(g.Members) == 1 {
		return g.Members[0].Label()
	}

	return fmt.Sprintf("%s (shared by %d references)", g.Members[0].ImageName, len(g.Members))
}

// fetchKey derives the identity of a registry lookup.
//
// The existing digest is part of the key on purpose. A pinned reference and an
// unpinned one naming the same image:tag take different paths through the
// fetcher, so merging them would produce a patch for the wrong reference.
func fetchKey(u core.ImageUpdate) string {
	return u.ImageName + "\x00" + u.OldTag + "\x00" + u.OldDigest
}

// groupUpdates collapses updates that would produce an identical registry
// result. A Compose file that reuses one image across many services otherwise
// triggers one registry round-trip per service.
func groupUpdates(updates []core.ImageUpdate) []fetchGroup {
	groups := make([]fetchGroup, 0, len(updates))
	seen := make(map[string]int, len(updates))

	for _, update := range updates {
		key := fetchKey(update)

		if index, ok := seen[key]; ok {
			groups[index].Members = append(groups[index].Members, update)

			continue
		}

		seen[key] = len(groups)
		groups = append(groups, fetchGroup{
			Representative: update,
			Members:        []core.ImageUpdate{update},
		})
	}

	return groups
}

// applyResultToAll copies a shared fetch outcome onto every member of a group.
//
// Only the resolved fields are copied. Each member keeps its own file path,
// line number, service name, original string, tag and digest, so the patcher
// still targets that member's exact line. Returning the representative as-is
// would rewrite every duplicate to the first service's line.
func applyResultToAll(result core.ImageUpdate, members []core.ImageUpdate) []core.ImageUpdate {
	applied := make([]core.ImageUpdate, 0, len(members))

	for _, member := range members {
		applied = append(applied, applyResult(result, member))
	}

	return applied
}

// applyResult copies the fields a fetch resolves onto a single member.
func applyResult(result, member core.ImageUpdate) core.ImageUpdate {
	member.CurrentDigest = result.CurrentDigest
	member.NewDigest = result.NewDigest
	member.NewTag = result.NewTag
	member.UpdateType = result.UpdateType
	member.Selected = result.Selected
	member.OldTagMissing = result.OldTagMissing
	member.NoCompatibleTags = result.NoCompatibleTags
	member.MajorTag = result.MajorTag

	return member
}
