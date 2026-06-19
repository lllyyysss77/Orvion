package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"gorm.io/gorm"
)

type telegramAuthKeyPatch struct {
	Name             *string    `json:"name,omitempty"`
	KeySuffix        *string    `json:"key_suffix,omitempty"`
	KeyTouched       bool       `json:"key_touched,omitempty"`
	Enabled          *bool      `json:"enabled,omitempty"`
	AllowAll         *bool      `json:"allow_all,omitempty"`
	Models           []string   `json:"models,omitempty"`
	ModelsTouched    bool       `json:"models_touched,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	ExpiresAtTouched bool       `json:"expires_at_touched,omitempty"`
	RPMLimit         *int       `json:"rpm_limit,omitempty"`
}

const telegramAgentAuthKeyListMaxLimit = 50

func buildTelegramCreateAuthKeyAction(ctx context.Context, chatID int64, args telegramAgentToolCallArgs) (telegramToolAction, error) {
	name := strings.TrimSpace(valueFromStringPtr(args.Name))
	if name == "" {
		return telegramToolAction{}, errors.New("请写明 API Key 项目名称")
	}

	enabled := true
	if args.Enabled != nil {
		enabled = *args.Enabled
	}
	allowAll := true
	if args.AllowAll != nil {
		allowAll = *args.AllowAll
	}
	allowedModels, err := resolveTelegramAuthKeyAllowedModels(ctx, allowAll, args.AuthModels, args.ModelKeywords, nil)
	if err != nil {
		return telegramToolAction{}, err
	}
	expiresAt, expiresTouched, err := parseTelegramAuthKeyExpiresAt(args.ExpiresAt, false)
	if err != nil {
		return telegramToolAction{}, err
	}
	rpmLimit := 0
	if args.RPMLimit != nil {
		if *args.RPMLimit < 0 {
			return telegramToolAction{}, errors.New("RPM 限制必须大于等于 0")
		}
		rpmLimit = *args.RPMLimit
	}

	patch := telegramAuthKeyPatch{
		Name:             &name,
		KeySuffix:        normalizeTelegramAuthKeySuffixPtr(args.KeySuffix, true),
		Enabled:          &enabled,
		AllowAll:         &allowAll,
		Models:           allowedModels,
		ModelsTouched:    true,
		ExpiresAt:        expiresAt,
		ExpiresAtTouched: expiresTouched,
		RPMLimit:         &rpmLimit,
	}
	return telegramToolAction{
		ChatID:       chatID,
		Kind:         telegramToolActionCreateAuthKey,
		AuthKeyPatch: patch,
		Summary:      "新增 API Key：" + name + "\n" + summarizeTelegramAuthKeyPatch(patch, true),
		CreatedAt:    time.Now(),
	}, nil
}

func buildTelegramUpdateAuthKeyAction(ctx context.Context, chatID int64, args telegramAgentToolCallArgs) (telegramToolAction, error) {
	target := cleanupTelegramToolTarget(args.Target)
	authKey, err := findTelegramAgentAuthKey(ctx, target)
	if err != nil {
		return telegramToolAction{}, err
	}

	var patch telegramAuthKeyPatch
	summaryParts := make([]string, 0, 6)
	if args.Name != nil {
		name := strings.TrimSpace(*args.Name)
		if name == "" {
			return telegramToolAction{}, errors.New("API Key 项目名称不能为空")
		}
		patch.Name = &name
		summaryParts = append(summaryParts, "名称改为 "+name)
	}
	if args.KeySuffix != nil {
		keySuffix := strings.TrimSpace(*args.KeySuffix)
		if keySuffix == "" {
			return telegramToolAction{}, errors.New("新的 Key 后缀不能为空")
		}
		patch.KeySuffix = normalizeTelegramAuthKeySuffixPtr(&keySuffix, false)
		patch.KeyTouched = true
		summaryParts = append(summaryParts, "Key 改为自定义后缀（已隐藏）")
	}
	if args.Enabled != nil {
		patch.Enabled = args.Enabled
		summaryParts = append(summaryParts, "状态改为 "+telegramAuthKeyEnabledLabel(*args.Enabled))
	}

	modelsTouched := len(args.AuthModels) > 0 || len(args.ModelKeywords) > 0
	if args.AllowAll != nil || modelsTouched {
		allowAll := modelsTouched == false
		if args.AllowAll != nil {
			allowAll = *args.AllowAll
		}
		if modelsTouched && args.AllowAll == nil {
			allowAll = false
		}
		if !allowAll && !modelsTouched && authKey.AllowAll == 1 {
			return telegramToolAction{}, errors.New("从无限制改为限制模型时，请提供 models 或 model_keywords")
		}
		allowedModels, err := resolveTelegramAuthKeyAllowedModels(ctx, allowAll, args.AuthModels, args.ModelKeywords, &authKey)
		if err != nil {
			return telegramToolAction{}, err
		}
		patch.AllowAll = &allowAll
		if allowAll || modelsTouched {
			patch.Models = allowedModels
			patch.ModelsTouched = true
		}
		summaryParts = append(summaryParts, "模型权限改为 "+formatTelegramAuthKeyScope(allowAll, allowedModels))
	}

	expiresAt, expiresTouched, err := parseTelegramAuthKeyExpiresAt(args.ExpiresAt, args.ClearExpiresAt)
	if err != nil {
		return telegramToolAction{}, err
	}
	if expiresTouched {
		patch.ExpiresAt = expiresAt
		patch.ExpiresAtTouched = true
		summaryParts = append(summaryParts, "有效期改为 "+formatTelegramAuthKeyExpiresAt(expiresAt))
	}
	if args.RPMLimit != nil {
		if *args.RPMLimit < 0 {
			return telegramToolAction{}, errors.New("RPM 限制必须大于等于 0")
		}
		patch.RPMLimit = args.RPMLimit
		summaryParts = append(summaryParts, "RPM 改为 "+formatTelegramAuthKeyRPMLimit(*args.RPMLimit))
	}
	if len(summaryParts) == 0 {
		return telegramToolAction{}, errors.New("请写明要修改的 API Key 字段")
	}

	return telegramToolAction{
		ChatID:       chatID,
		Kind:         telegramToolActionUpdateAuthKey,
		TargetID:     authKey.ID,
		TargetName:   authKey.Name,
		AuthKeyPatch: patch,
		Summary:      fmt.Sprintf("更新 API Key：%s\n变更：%s", authKey.Name, strings.Join(summaryParts, "；")),
		CreatedAt:    time.Now(),
	}, nil
}

func createTelegramAgentAuthKey(ctx context.Context, patch telegramAuthKeyPatch) (string, error) {
	if patch.Name == nil || strings.TrimSpace(*patch.Name) == "" {
		return "", errors.New("API Key 项目名称不能为空")
	}
	key, err := buildUniqueTelegramAuthKeyValue(ctx, patch.KeySuffix, 0)
	if err != nil {
		return "", err
	}

	enabled := true
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	allowAll := true
	if patch.AllowAll != nil {
		allowAll = *patch.AllowAll
	}
	authKey := models.AuthKey{
		Name:      strings.TrimSpace(*patch.Name),
		Key:       key,
		Status:    boolToInt(enabled),
		AllowAll:  boolToInt(allowAll),
		Models:    marshalTelegramAuthKeyModels(patch.Models),
		ExpiresAt: patch.ExpiresAt,
		RpmLimit:  valueFromIntPtr(patch.RPMLimit, 0),
	}
	if allowAll {
		authKey.Models = "[]"
	}
	if err := models.DB.WithContext(ctx).Create(&authKey).Error; err != nil {
		return "", err
	}
	notifyTelegramAgentAuthKeyChanged()

	return strings.Join([]string{
		"已新增 API Key",
		"项目：" + authKey.Name,
		"Key：" + maskTelegramAuthKeyValue(authKey.Key),
		"状态：" + telegramAuthKeyEnabledLabel(enabled),
		"权限：" + formatTelegramAuthKeyScope(allowAll, patch.Models),
		"RPM：" + formatTelegramAuthKeyRPMLimit(authKey.RpmLimit),
		"有效期：" + formatTelegramAuthKeyExpiresAt(authKey.ExpiresAt),
		"完整 Key 请到 API Key 管理页面查看或复制。",
	}, "\n"), nil
}

func listTelegramAgentAuthKeys(ctx context.Context, args telegramAgentToolCallArgs) (string, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > telegramAgentAuthKeyListMaxLimit {
		limit = telegramAgentAuthKeyListMaxLimit
	}

	query := models.DB.WithContext(ctx).Model(&models.AuthKey{})
	keyword := strings.TrimSpace(args.Query)
	if keyword != "" {
		query = query.Where("LOWER(name) LIKE ?", "%"+strings.ToLower(keyword)+"%")
	}

	switch strings.ToLower(strings.TrimSpace(args.Status)) {
	case "", "all":
	case "enabled":
		query = query.Where("status = ?", 1)
	case "disabled":
		query = query.Where("status = ?", 0)
	default:
		return "", errors.New("状态筛选只支持 all、enabled、disabled")
	}

	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return "", err
	}
	if total == 0 {
		return "暂无匹配 API Key", nil
	}

	authKeys := make([]models.AuthKey, 0, limit)
	if err := query.Session(&gorm.Session{}).
		Order("LOWER(name) ASC").
		Order("id ASC").
		Limit(limit).
		Find(&authKeys).Error; err != nil {
		return "", err
	}

	lines := []string{fmt.Sprintf("API Key 列表（显示 %d/%d 个）", len(authKeys), total)}
	for index, authKey := range authKeys {
		name := strings.TrimSpace(authKey.Name)
		if name == "" {
			name = "未命名"
		}
		lines = append(lines,
			fmt.Sprintf("%d. 项目：%s", index+1, name),
			"   Key："+maskTelegramAuthKeyValue(authKey.Key),
			"   状态："+telegramAuthKeyEnabledLabel(authKey.Status == 1),
			"   权限："+formatTelegramAuthKeyScope(authKey.AllowAll == 1, parseTelegramAuthKeyModels(authKey.Models)),
			"   RPM："+formatTelegramAuthKeyRPMLimit(authKey.RpmLimit),
			fmt.Sprintf("   使用次数：%d", authKey.UsageCount),
			fmt.Sprintf("   已消耗金额：%s", formatTelegramAuthKeyCost(authKey.TotalCost)),
			"   最后使用："+formatTelegramAuthKeyLastUsedAt(authKey.LastUsedAt),
			"   有效期："+formatTelegramAuthKeyExpiresAt(authKey.ExpiresAt),
		)
	}
	if total > int64(len(authKeys)) {
		lines = append(lines, fmt.Sprintf("还有 %d 个未显示，可用 query 或 limit 缩小范围。", total-int64(len(authKeys))))
	}
	return strings.Join(lines, "\n"), nil
}

func updateTelegramAgentAuthKey(ctx context.Context, authKeyID uint, patch telegramAuthKeyPatch) (string, error) {
	current, err := getTelegramAgentAuthKeyByID(ctx, authKeyID)
	if err != nil {
		return "", err
	}

	updates := make(map[string]any)
	changedParts := make([]string, 0, 6)
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return "", errors.New("API Key 项目名称不能为空")
		}
		updates["name"] = name
		changedParts = append(changedParts, "名称")
	}
	if patch.KeyTouched {
		key, err := buildUniqueTelegramAuthKeyValue(ctx, patch.KeySuffix, authKeyID)
		if err != nil {
			return "", err
		}
		updates["key"] = key
		changedParts = append(changedParts, "Key")
	}
	if patch.Enabled != nil {
		updates["status"] = boolToInt(*patch.Enabled)
		changedParts = append(changedParts, "状态")
	}
	if patch.AllowAll != nil {
		updates["allow_all"] = boolToInt(*patch.AllowAll)
		changedParts = append(changedParts, "模型权限")
		if *patch.AllowAll {
			updates["models"] = "[]"
		} else if patch.ModelsTouched {
			updates["models"] = marshalTelegramAuthKeyModels(patch.Models)
		}
	}
	if patch.ExpiresAtTouched {
		updates["expires_at"] = patch.ExpiresAt
		changedParts = append(changedParts, "有效期")
	}
	if patch.RPMLimit != nil {
		updates["rpm_limit"] = *patch.RPMLimit
		changedParts = append(changedParts, "RPM")
	}
	if len(updates) == 0 {
		return "", errors.New("没有可更新的 API Key 字段")
	}
	if err := models.DB.WithContext(ctx).
		Model(&models.AuthKey{}).
		Where("id = ?", authKeyID).
		Updates(updates).Error; err != nil {
		return "", err
	}
	notifyTelegramAgentAuthKeyChanged()

	updated, err := getTelegramAgentAuthKeyByID(ctx, authKeyID)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"已更新 API Key",
		"项目：" + updated.Name,
		"Key：" + maskTelegramAuthKeyValue(updated.Key),
		"修改：" + strings.Join(orderedUniqueStrings(changedParts), "、"),
		"状态：" + telegramAuthKeyEnabledLabel(updated.Status == 1),
		"权限：" + formatTelegramAuthKeyScope(updated.AllowAll == 1, parseTelegramAuthKeyModels(updated.Models)),
		"RPM：" + formatTelegramAuthKeyRPMLimit(updated.RpmLimit),
		"有效期：" + formatTelegramAuthKeyExpiresAt(updated.ExpiresAt),
		"原项目：" + current.Name,
	}, "\n"), nil
}

func resolveTelegramAuthKeyAllowedModels(ctx context.Context, allowAll bool, exactNames []string, keywords []string, existing *models.AuthKey) ([]string, error) {
	if allowAll {
		return nil, nil
	}

	modelNames := make([]string, 0, len(exactNames)+len(keywords)*3)
	for _, rawName := range exactNames {
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue
		}
		model, err := findTelegramAgentModel(ctx, name)
		if err != nil {
			return nil, err
		}
		modelNames = append(modelNames, model.Name)
	}
	for _, rawKeyword := range keywords {
		keyword := cleanupTelegramToolTarget(rawKeyword)
		if keyword == "" {
			continue
		}
		rows, err := findTelegramAgentModelsByKeyword(ctx, keyword)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			modelNames = append(modelNames, row.Name)
		}
	}
	modelNames = orderedUniqueStrings(modelNames)
	if len(modelNames) > 0 {
		return modelNames, nil
	}
	if existing != nil && existing.AllowAll == 0 {
		modelNames = parseTelegramAuthKeyModels(existing.Models)
		if len(modelNames) > 0 {
			return modelNames, nil
		}
	}
	return nil, errors.New("限制模型时请至少提供一个模型名称或模型关键词")
}

func findTelegramAgentAuthKey(ctx context.Context, target string) (models.AuthKey, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return models.AuthKey{}, errors.New("请写明 API Key 项目名称")
	}
	if id, ok := parseTelegramToolID(target); ok {
		return getTelegramAgentAuthKeyByID(ctx, id)
	}

	var exact []models.AuthKey
	if err := models.DB.WithContext(ctx).
		Where("LOWER(name) = ?", strings.ToLower(target)).
		Order("id ASC").
		Find(&exact).Error; err != nil {
		return models.AuthKey{}, err
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return models.AuthKey{}, ambiguousTelegramToolTargetError("API Key", exactAuthKeyNames(exact))
	}

	var fuzzy []models.AuthKey
	if err := models.DB.WithContext(ctx).
		Where("name LIKE ?", "%"+target+"%").
		Order("LOWER(name) ASC").
		Order("id ASC").
		Limit(6).
		Find(&fuzzy).Error; err != nil {
		return models.AuthKey{}, err
	}
	if len(fuzzy) == 0 {
		return models.AuthKey{}, fmt.Errorf("未找到 API Key：%s", target)
	}
	if len(fuzzy) > 1 {
		return models.AuthKey{}, ambiguousTelegramToolTargetError("API Key", exactAuthKeyNames(fuzzy))
	}
	return fuzzy[0], nil
}

func getTelegramAgentAuthKeyByID(ctx context.Context, id uint) (models.AuthKey, error) {
	var authKey models.AuthKey
	if err := models.DB.WithContext(ctx).Where("id = ?", id).First(&authKey).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.AuthKey{}, errors.New("未找到对应 API Key")
		}
		return models.AuthKey{}, err
	}
	return authKey, nil
}

func buildUniqueTelegramAuthKeyValue(ctx context.Context, keySuffix *string, excludeID uint) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		key, err := buildTelegramAuthKeyValue(keySuffix)
		if err != nil {
			return "", err
		}
		var count int64
		query := models.DB.WithContext(ctx).Model(&models.AuthKey{}).Where(models.ColumnEquals("key"), key)
		if excludeID > 0 {
			query = query.Where("id <> ?", excludeID)
		}
		if err := query.Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return key, nil
		}
		if keySuffix != nil && strings.TrimSpace(*keySuffix) != "" {
			return "", errors.New("API Key 已存在")
		}
	}
	return "", errors.New("自动生成 API Key 连续冲突，请重试")
}

func buildTelegramAuthKeyValue(keySuffix *string) (string, error) {
	suffix := ""
	if keySuffix != nil {
		suffix = strings.TrimSpace(*keySuffix)
	}
	if suffix == "" {
		randomKey, err := pkg.GenerateRandomCharsKey(36)
		if err != nil {
			return "", errors.New("生成 API Key 失败: " + err.Error())
		}
		return consts.KeyPrefix + randomKey, nil
	}

	suffix = strings.TrimPrefix(suffix, "sk-")
	if strings.TrimSpace(suffix) == "" {
		return "", errors.New("自定义 Key 后缀不能为空")
	}
	if strings.ContainsAny(suffix, " \t\r\n") {
		return "", errors.New("自定义 Key 后缀不能包含空白字符")
	}
	if len(suffix) > 128 {
		return "", errors.New("自定义 Key 后缀不能超过 128 个字符")
	}
	return "sk-" + suffix, nil
}

func parseTelegramAuthKeyExpiresAt(raw *string, clear bool) (*time.Time, bool, error) {
	if clear {
		return nil, true, nil
	}
	if raw == nil {
		return nil, false, nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, true, nil
	}
	parsed, err := parseTelegramAgentLogTime(value, true)
	if err != nil {
		return nil, false, errors.New("有效期时间格式无效")
	}
	return &parsed, true, nil
}

func normalizeTelegramAuthKeySuffixPtr(value *string, emptyMeansAuto bool) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" && emptyMeansAuto {
		return nil
	}
	trimmed = strings.TrimPrefix(trimmed, "sk-")
	return &trimmed
}

func parseTelegramAuthKeyModels(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return orderedUniqueStrings(values)
}

func marshalTelegramAuthKeyModels(values []string) string {
	values = orderedUniqueStrings(values)
	if len(values) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func summarizeTelegramAuthKeyPatch(patch telegramAuthKeyPatch, creating bool) string {
	lines := make([]string, 0, 5)
	if patch.Enabled != nil {
		lines = append(lines, "状态："+telegramAuthKeyEnabledLabel(*patch.Enabled))
	}
	if patch.AllowAll != nil {
		lines = append(lines, "权限："+formatTelegramAuthKeyScope(*patch.AllowAll, patch.Models))
	}
	if patch.RPMLimit != nil {
		lines = append(lines, "RPM："+formatTelegramAuthKeyRPMLimit(*patch.RPMLimit))
	}
	if patch.ExpiresAtTouched || creating {
		lines = append(lines, "有效期："+formatTelegramAuthKeyExpiresAt(patch.ExpiresAt))
	}
	if patch.KeySuffix != nil {
		lines = append(lines, "Key：自定义后缀（已隐藏）")
	} else if creating {
		lines = append(lines, "Key：自动生成")
	}
	return strings.Join(lines, "\n")
}

func formatTelegramAuthKeyScope(allowAll bool, models []string) string {
	if allowAll {
		return "全部模型"
	}
	models = orderedUniqueStrings(models)
	if len(models) == 0 {
		return "指定模型（沿用当前列表）"
	}
	return fmt.Sprintf("指定模型（%d 个）：%s", len(models), summarizeTelegramAuthKeyModels(models))
}

func summarizeTelegramAuthKeyModels(models []string) string {
	models = orderedUniqueStrings(models)
	if len(models) == 0 {
		return "无"
	}
	if len(models) > telegramAgentToolListLimit {
		return strings.Join(models[:telegramAgentToolListLimit], "、") + fmt.Sprintf(" 等 %d 个", len(models))
	}
	return strings.Join(models, "、")
}

func formatTelegramAuthKeyExpiresAt(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "永久有效"
	}
	return value.Local().Format("2006-01-02 15:04:05")
}

func formatTelegramAuthKeyRPMLimit(value int) string {
	if value <= 0 {
		return "无限制"
	}
	return fmt.Sprintf("%d", value)
}

func formatTelegramAuthKeyCost(value float64) string {
	return fmt.Sprintf("$%.4f", value)
}

func formatTelegramAuthKeyLastUsedAt(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "从未使用"
	}
	return value.Local().Format("2006-01-02 15:04:05")
}

func telegramAuthKeyEnabledLabel(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "禁用"
}

func maskTelegramAuthKeyValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "已隐藏"
	}
	if len([]rune(value)) <= 12 {
		return "已隐藏"
	}
	runes := []rune(value)
	return string(runes[:6]) + "..." + string(runes[len(runes)-6:])
}

func exactAuthKeyNames(keys []models.AuthKey) []string {
	names := make([]string, 0, len(keys))
	for _, key := range keys {
		names = append(names, key.Name)
	}
	return names
}

func valueFromStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func valueFromIntPtr(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}
