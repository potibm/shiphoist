package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/potibm/shiphoist/internal/core"
)

const calVerThresholdYear = 1900

type DefaultFetcher struct {
	client RegistryClient
}

func NewDefaultFetcher(client RegistryClient) *DefaultFetcher {
	return &DefaultFetcher{
		client: client,
	}
}

func (f *DefaultFetcher) FetchUpdate(ctx context.Context, current core.ImageUpdate) (core.ImageUpdate, error) {
	refString := fmt.Sprintf("%s:%s", current.ImageName, current.OldTag)

	oldDigest, err := f.client.GetDigest(ctx, refString)
	if err != nil {
		var terr *transport.Error
		if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
			current.OldTagMissing = true
		} else {
			return current, fmt.Errorf("failed to fetch metadata: %w", err)
		}
	}

	current.CurrentDigest = oldDigest
	current.NewDigest = oldDigest
	current.NewTag = current.OldTag
	current.UpdateType = core.UpdateTypeNone
	current.Selected = false

	oldParsed, err := ParseTag(current.OldTag)
	if err != nil {
		return f.handleNonSemverTag(ctx, current)
	}

	allTags, err := f.listTags(ctx, current.ImageName)
	if err != nil {
		return current, err
	}

	// Filter by BaseSuffix (allows different build/revision IDs)
	baseCandidates := allTags.FilterByBaseSuffix(oldParsed.BaseSuffix)

	isCalVer := oldParsed.SemVer.Major() >= calVerThresholdYear

	candidates := selectCandidates(baseCandidates, oldParsed, isCalVer)

	if len(candidates) == 0 {
		current.NoCompatibleTags = true

		return current, nil
	}

	candidates = candidates.SortBySemver()

	sameMajor, majorBump := classifyCandidates(candidates, oldParsed, isCalVer)

	// Both rules can fail for the same reason: a reference the registry will
	// not resolve a digest for cannot be pinned, so it has to be reported as a
	// failure rather than as an update that was applied.
	if err := f.applyUpdateRules(ctx, &current, sameMajor, baseCandidates, oldParsed, isCalVer); err != nil {
		return current, err
	}

	if majorBump != nil {
		current.MajorTag = majorBump.Raw
	}

	return current, nil
}

// applyUpdateRules runs the two rules that turn a candidate into an update.
//
// Each reports an error instead of leaving an unpinnable update behind, so the
// caller only has to propagate it: the alternative is an update sitting in the
// report with no digest, which the patcher skips without a word.
func (f *DefaultFetcher) applyUpdateRules(
	ctx context.Context,
	current *core.ImageUpdate,
	sameMajor, baseCandidates TagList,
	oldParsed Tag,
	isCalVer bool,
) error {
	if len(sameMajor) > 0 {
		if err := f.processSameMajorUpdate(ctx, current, sameMajor, oldParsed); err != nil {
			return err
		}
	}

	// Successor rule: when tag is missing and no greater same-major candidate,
	// look for a coarser-precision tag that's a version prefix of the current tag
	if current.OldTagMissing && !current.Selected {
		return f.applySuccessorRule(ctx, current, baseCandidates, oldParsed, isCalVer)
	}

	return nil
}

// selectCandidates narrows the available tags to the ones eligible for
// ranking.
//
// Tags at the same precision as the current one are preferred, so a pinned
// 1.2.3 is not handed a 1.2.4.1 that happens to sort higher. That preference is
// not a filter: when the same-precision set holds nothing newer in the current
// major, the search widens to every tag sharing the base suffix. Without the
// widening a channel tag such as 22-alpine or 8.8 could never see 22.23-alpine
// or 8.10.2, and would report "Up to date" however far behind it was.
//
// Precision constrains the shape of a candidate, not the size of the jump: the
// widest same-major tag wins, so 1.2.3 still becomes 1.4.0 when no 1.2.4 exists.
func selectCandidates(baseCandidates TagList, oldParsed Tag, isCalVer bool) TagList {
	strict := baseCandidates.FilterByPrecision(oldParsed.Precision)
	if hasNewerSameMajor(strict, oldParsed, isCalVer) {
		return preferVPrefix(strict, oldParsed.HasVPrefix)
	}

	return preferVPrefix(baseCandidates, oldParsed.HasVPrefix)
}

// preferVPrefix keeps the tags spelled the same way as the current one, so a
// v-prefixed tag is never handed a bare replacement. The preference is dropped
// when nothing matches, because an update in the other style still beats none.
func preferVPrefix(candidates TagList, hasVPrefix bool) TagList {
	samePrefix := candidates.FilterByVPrefix(hasVPrefix)
	if len(samePrefix) > 0 {
		return samePrefix
	}

	return candidates
}

// hasNewerSameMajor reports whether the candidates hold a tag of the current
// major that is newer than the current tag and shares its CalVer schema.
//
// This is a yes/no question about the whole set, so it scans for the newest
// qualifying version rather than materialising the classified list: the caller
// classifies again after preferVPrefix, on a set this one must not influence.
func hasNewerSameMajor(candidates TagList, oldParsed Tag, isCalVer bool) bool {
	for i := range candidates {
		tag := &candidates[i]

		if isCalVer != (tag.SemVer.Major() >= calVerThresholdYear) {
			continue
		}

		if tag.SemVer.Major() != oldParsed.SemVer.Major() {
			continue
		}

		if tag.SemVer.GreaterThan(oldParsed.SemVer) {
			return true
		}
	}

	return false
}

// classifyCandidates splits candidates into those in the current major and the
// newest one past it, dropping anything from a different CalVer schema.
func classifyCandidates(candidates TagList, oldParsed Tag, isCalVer bool) (TagList, *Tag) {
	var (
		sameMajor TagList
		majorBump *Tag
	)

	for i := range candidates {
		tag := &candidates[i]
		tagIsCalVer := tag.SemVer.Major() >= calVerThresholdYear

		if isCalVer != tagIsCalVer {
			continue
		}

		if tag.SemVer.Major() == oldParsed.SemVer.Major() {
			sameMajor = append(sameMajor, *tag)
		} else if tag.SemVer.Major() > oldParsed.SemVer.Major() {
			majorBump = tag
		}
	}

	return sameMajor, majorBump
}

// listTags fetches all available tags for a given image repository.
func (f *DefaultFetcher) listTags(ctx context.Context, imageName string) (TagList, error) {
	rawTags, err := f.client.ListTags(ctx, imageName)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tags for %s: %w", imageName, err)
	}

	return NewTagListFromStrings(rawTags), nil
}

// handleNonSemverTag resolves an image whose current tag has no SemVer shape.
//
// The tag listing is only attempted when the current tag is gone from the
// registry, because that is the only case where the listing can change the
// outcome. A failing listing is reported rather than dropped: the caller
// otherwise has no way to tell "no compatible tags" from "the registry was
// unreachable", and an unreachable registry would read as up to date.
func (f *DefaultFetcher) handleNonSemverTag(ctx context.Context, current core.ImageUpdate) (core.ImageUpdate, error) {
	if current.OldTagMissing {
		allTags, err := f.listTags(ctx, current.ImageName)
		if err != nil {
			return current, err
		}

		if len(allTags) == 0 {
			current.NoCompatibleTags = true
		}
	}

	if !current.OldTagMissing && current.OldDigest != current.CurrentDigest {
		current.UpdateType = core.UpdateTypePatch
		current.Selected = true
	}

	return current, nil
}

// processSameMajorUpdate picks the newest candidate inside the current major.
//
// It returns an error when the digest cannot be resolved: the update is then
// reported as a failure instead of as ready to apply. Without a digest there is
// nothing to pin, and the patcher would skip the reference while the run
// claimed a write.
func (f *DefaultFetcher) processSameMajorUpdate(
	ctx context.Context,
	current *core.ImageUpdate,
	sameMajor TagList,
	oldParsed Tag,
) error {
	safeNewest := sameMajor[len(sameMajor)-1]
	if !safeNewest.SemVer.GreaterThan(oldParsed.SemVer) {
		return nil
	}

	current.NewTag = safeNewest.Raw
	current.Selected = true

	if safeNewest.SemVer.Minor() > oldParsed.SemVer.Minor() {
		current.UpdateType = core.UpdateTypeMinor
	} else {
		current.UpdateType = core.UpdateTypePatch
	}

	newDigest, err := f.resolveNewDigest(ctx, current)
	if err != nil {
		return err
	}

	current.NewDigest = newDigest

	return nil
}

// applySuccessorRule looks for a coarser-precision tag that the current tag is
// a refinement of, for a reference whose exact tag is gone from the registry.
//
// It returns an error when the digest of the successor cannot be resolved, for
// the same reason as processSameMajorUpdate: an unpinnable update must not be
// reported as applied.
func (f *DefaultFetcher) applySuccessorRule(
	ctx context.Context,
	current *core.ImageUpdate,
	baseCandidates TagList,
	oldParsed Tag,
	isCalVer bool,
) error {
	successorCandidates := baseCandidates.FilterByVPrefix(oldParsed.HasVPrefix).SortBySemver()

	var successorSameMajor TagList

	for i := range successorCandidates {
		tag := &successorCandidates[i]

		tagIsCalVer := tag.SemVer.Major() >= calVerThresholdYear
		if isCalVer != tagIsCalVer {
			continue
		}

		if tag.SemVer.Major() == oldParsed.SemVer.Major() {
			successorSameMajor = append(successorSameMajor, *tag)
		}
	}

	if len(successorSameMajor) == 0 {
		return nil
	}

	for i := len(successorSameMajor) - 1; i >= 0; i-- {
		candidate := successorSameMajor[i]
		if !strings.HasPrefix(current.OldTag, candidate.Raw+".") {
			continue
		}

		current.NewTag = candidate.Raw
		current.Selected = true
		current.UpdateType = core.UpdateTypePatch

		newDigest, err := f.resolveNewDigest(ctx, current)
		if err != nil {
			return err
		}

		current.NewDigest = newDigest

		return nil
	}

	return nil
}

// resolveNewDigest fetches the digest of the reference the update points at.
func (f *DefaultFetcher) resolveNewDigest(ctx context.Context, current *core.ImageUpdate) (string, error) {
	refString := fmt.Sprintf("%s:%s", current.ImageName, current.NewTag)

	newDigest, err := f.client.GetDigest(ctx, refString)
	if err != nil {
		return "", fmt.Errorf("failed to resolve digest for %s: %w", refString, err)
	}

	return newDigest, nil
}
