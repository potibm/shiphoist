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

	rawTags, err := f.client.ListTags(ctx, current.ImageName)
	if err != nil {
		return current, fmt.Errorf("failed to fetch tags: %w", err)
	}

	allTags := NewTagListFromStrings(rawTags)

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

	if len(sameMajor) > 0 {
		f.processSameMajorUpdate(ctx, &current, sameMajor, oldParsed)
	}

	// Successor rule: when tag is missing and no greater same-major candidate,
	// look for a coarser-precision tag that's a version prefix of the current tag
	if current.OldTagMissing && !current.Selected {
		f.applySuccessorRule(ctx, &current, baseCandidates, oldParsed, isCalVer)
	}

	if majorBump != nil {
		current.MajorTag = majorBump.Raw
	}

	return current, nil
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
func hasNewerSameMajor(candidates TagList, oldParsed Tag, isCalVer bool) bool {
	if len(candidates) == 0 {
		return false
	}

	sameMajor, _ := classifyCandidates(candidates.SortBySemver(), oldParsed, isCalVer)
	if len(sameMajor) == 0 {
		return false
	}

	return sameMajor[len(sameMajor)-1].SemVer.GreaterThan(oldParsed.SemVer)
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
		return nil, fmt.Errorf("failed to list tags for repository %s: %w", imageName, err)
	}

	return NewTagListFromStrings(rawTags), nil
}

func (f *DefaultFetcher) handleNonSemverTag(ctx context.Context, current core.ImageUpdate) (core.ImageUpdate, error) {
	if current.OldTagMissing {
		rawTags, listErr := f.client.ListTags(ctx, current.ImageName)
		if listErr == nil {
			allTags := NewTagListFromStrings(rawTags)
			if len(allTags) == 0 {
				current.NoCompatibleTags = true
			}
		}
	}

	if !current.OldTagMissing && current.OldDigest != current.CurrentDigest {
		current.UpdateType = core.UpdateTypePatch
		current.Selected = true
	}

	return current, nil
}

func (f *DefaultFetcher) processSameMajorUpdate(
	ctx context.Context,
	current *core.ImageUpdate,
	sameMajor TagList,
	oldParsed Tag,
) {
	safeNewest := sameMajor[len(sameMajor)-1]
	if !safeNewest.SemVer.GreaterThan(oldParsed.SemVer) {
		return
	}

	current.NewTag = safeNewest.Raw
	current.Selected = true

	if safeNewest.SemVer.Minor() > oldParsed.SemVer.Minor() {
		current.UpdateType = core.UpdateTypeMinor
	} else {
		current.UpdateType = core.UpdateTypePatch
	}

	newRefString := fmt.Sprintf("%s:%s", current.ImageName, current.NewTag)
	if newDigest, err := f.client.GetDigest(ctx, newRefString); err == nil {
		current.NewDigest = newDigest
	}
}

func (f *DefaultFetcher) applySuccessorRule(
	ctx context.Context,
	current *core.ImageUpdate,
	baseCandidates TagList,
	oldParsed Tag,
	isCalVer bool,
) {
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
		return
	}

	for i := len(successorSameMajor) - 1; i >= 0; i-- {
		candidate := successorSameMajor[i]
		if !strings.HasPrefix(current.OldTag, candidate.Raw+".") {
			continue
		}

		current.NewTag = candidate.Raw
		current.Selected = true
		current.UpdateType = core.UpdateTypePatch

		newRefString := fmt.Sprintf("%s:%s", current.ImageName, current.NewTag)
		if newDigest, err := f.client.GetDigest(ctx, newRefString); err == nil {
			current.NewDigest = newDigest
		}

		break
	}
}
