package service

import (
	"context"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/providers"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

func ModelsByTypes(ctx context.Context, modelTypes ...string) ([]models.Model, error) {
	llmproviders, err := gorm.G[models.Provider](models.DB).Find(ctx)
	if err != nil {
		return nil, err
	}
	if len(modelTypes) > 0 {
		llmproviders = lo.Filter(llmproviders, func(provider models.Provider, _ int) bool {
			nativeStyle := providers.ResolveStyle("", provider.Config)
			for _, style := range modelTypes {
				switch style {
				case consts.StyleOpenAI, consts.StyleOpenAIEmbeddings, consts.StyleOpenAIRes:
					if nativeStyle == consts.StyleOpenAI {
						return true
					}
				case consts.StyleAnthropic:
					if nativeStyle == consts.StyleAnthropic {
						return true
					}
				case consts.StyleGemini, consts.StyleGeminiEmbeddings:
					if nativeStyle == consts.StyleGemini {
						return true
					}
				}
			}
			return false
		})
	}
	if len(llmproviders) == 0 {
		return []models.Model{}, nil
	}

	modelWithProviders, err := gorm.G[models.ModelWithProvider](models.DB).Where("provider_id IN ?", lo.Map(llmproviders, func(p models.Provider, _ int) uint { return p.ID })).Find(ctx)
	if err != nil {
		return nil, err
	}
	if len(modelWithProviders) == 0 {
		return []models.Model{}, nil
	}

	modelIDs := lo.Uniq(lo.Map(modelWithProviders, func(mp models.ModelWithProvider, _ int) uint { return mp.ModelID }))
	models, err := gorm.G[models.Model](models.DB).Where("id IN ?", modelIDs).Find(ctx)
	if err != nil {
		return nil, err
	}
	return models, nil
}
