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
		// Non-semver path: best-effort ListTags for accurate NoCompatibleTags
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

	rawTags, err := f.client.ListTags(ctx, current.ImageName)
	if err != nil {
		return current, fmt.Errorf("failed to fetch tags: %w", err)
	}

	allTags := NewTagListFromStrings(rawTags)

	// Filter by BaseSuffix (allows different build/revision IDs)
	baseCandidates := allTags.FilterByBaseSuffix(oldParsed.BaseSuffix)

	// Precision fallback: if zero candidates at exact precision, retry ignoring precision
	candidates := baseCandidates.FilterByPrecision(oldParsed.Precision)
	if len(candidates) == 0 {
		candidates = baseCandidates
	}

	if len(candidates) == 0 {
		current.NoCompatibleTags = true

		return current, nil
	}

	samePrefix := candidates.FilterByVPrefix(oldParsed.HasVPrefix)
	if len(samePrefix) > 0 {
		candidates = samePrefix
	}

	candidates = candidates.SortBySemver()

	isCalVer := oldParsed.SemVer.Major() >= 1900

	var (
		sameMajor TagList
		majorBump *Tag
	)

	for i := range candidates {
		tag := &candidates[i]
		tagIsCalVer := tag.SemVer.Major() >= 1900

		// CalVer boundary: only consider candidates within the same schema
		if isCalVer != tagIsCalVer {
			continue
		}

		if tag.SemVer.Major() == oldParsed.SemVer.Major() {
			sameMajor = append(sameMajor, *tag)
		} else if tag.SemVer.Major() > oldParsed.SemVer.Major() {
			majorBump = tag
		}
	}

	if len(sameMajor) > 0 {
		safeNewest := sameMajor[len(sameMajor)-1]
		if safeNewest.SemVer.GreaterThan(oldParsed.SemVer) {
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
	}

	// Successor rule: when tag is missing and no greater same-major candidate,
	// look for a coarser-precision tag that's a version prefix of the current tag
	if current.OldTagMissing && !current.Selected {
		// Use baseCandidates (any precision) for successor search
		successorCandidates := baseCandidates.FilterByVPrefix(oldParsed.HasVPrefix).SortBySemver()

		var successorSameMajor TagList

		for i := range successorCandidates {
			tag := &successorCandidates[i]

			tagIsCalVer := tag.SemVer.Major() >= 1900
			if isCalVer != tagIsCalVer {
				continue
			}

			if tag.SemVer.Major() == oldParsed.SemVer.Major() {
				successorSameMajor = append(successorSameMajor, *tag)
			}
		}

		if len(successorSameMajor) > 0 {
			for i := len(successorSameMajor) - 1; i >= 0; i-- {
				candidate := successorSameMajor[i]
				// Check if candidate is a version prefix of current (e.g., "22.04" is prefix of "22.04.2")
				if strings.HasPrefix(current.OldTag, candidate.Raw+".") {
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
		}
	}

	if majorBump != nil {
		current.MajorTag = majorBump.Raw
	}

	return current, nil
}

// listTags fetches all available tags for a given image repository.
func (f *DefaultFetcher) listTags(ctx context.Context, imageName string) (TagList, error) {
	rawTags, err := f.client.ListTags(ctx, imageName)
	if err != nil {
		return nil, fmt.Errorf("failed to list tags for repository %s: %w", imageName, err)
	}

	return NewTagListFromStrings(rawTags), nil
}
