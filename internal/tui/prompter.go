package tui

import (
	"fmt"
	"sort"

	"github.com/charmbracelet/huh"
	"github.com/potibm/shiphoist/internal/core"
)

const (
	scoreMajor = 4
	scoreMinor = 3
	scorePatch = 2
	scoreNone  = 1
	scoreOther = 0
)

type HuhPrompter struct{}

func NewHuhPrompter() *HuhPrompter {
	return &HuhPrompter{}
}

func (h *HuhPrompter) SelectUpdates(updates []core.ImageUpdate) ([]core.ImageUpdate, error) {
	if len(updates) == 0 {
		return updates, nil
	}

	sort.Slice(updates, func(i, j int) bool {
		return scoreUpdateType(updates[i].UpdateType) > scoreUpdateType(updates[j].UpdateType)
	})

	var (
		selectedImageNames []string
		options            []huh.Option[string]
	)

	for _, u := range updates {
		label := fmt.Sprintf("%s (%s -> %s)", u.ImageName, u.OldTag, u.NewTag)
		isSafeToAutoUpdate := u.UpdateType != core.UpdateTypeMajor

		options = append(options, huh.NewOption(label, u.ImageName).Selected(isSafeToAutoUpdate))
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("🚢 Which updates should be applied?").
				Options(options...).
				Value(&selectedImageNames),
		),
	)

	if err := form.Run(); err != nil {
		return nil, err
	}

	var filteredUpdates []core.ImageUpdate

	for _, u := range updates {
		for _, selectedName := range selectedImageNames {
			if u.ImageName == selectedName {
				filteredUpdates = append(filteredUpdates, u)

				break
			}
		}
	}

	return filteredUpdates, nil
}

func scoreUpdateType(t core.UpdateType) int {
	switch t {
	case core.UpdateTypeMajor:
		return scoreMajor
	case core.UpdateTypeMinor:
		return scoreMinor
	case core.UpdateTypePatch:
		return scorePatch
	case core.UpdateTypeNone:
		return scoreNone
	default:
		return scoreOther
	}
}
