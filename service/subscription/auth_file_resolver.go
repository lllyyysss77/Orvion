package subscription

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type credentialFileRef struct {
	Name         string
	Path         string
	RelativePath string
	ModTime      time.Time
	depth        int
}

// collectCredentialFiles 递归扫描 authDir，返回去重后的凭据文件：
// - 仅保留匹配 pattern 的文件名；
// - 若同名文件在多处出现，优先更浅层目录（根目录优先），同层按相对路径字典序；
// - 最终结果按修改时间倒序（新文件优先）。
func collectCredentialFiles(authDir string, pattern *regexp.Regexp) ([]credentialFileRef, error) {
	authDir = strings.TrimSpace(authDir)
	if authDir == "" {
		return []credentialFileRef{}, nil
	}

	grouped := make(map[string][]credentialFileRef)
	err := filepath.WalkDir(authDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// 忽略单个路径错误，尽量继续扫描其他文件。
			return nil
		}
		if d == nil || d.IsDir() {
			return nil
		}

		name := d.Name()
		if pattern == nil || !pattern.MatchString(name) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		relPath, relErr := filepath.Rel(authDir, path)
		if relErr != nil {
			relPath = name
		}
		relPath = filepath.ToSlash(relPath)

		grouped[name] = append(grouped[name], credentialFileRef{
			Name:         name,
			Path:         path,
			RelativePath: relPath,
			ModTime:      info.ModTime().UTC(),
			depth:        strings.Count(relPath, "/"),
		})
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []credentialFileRef{}, nil
		}
		return nil, err
	}

	result := make([]credentialFileRef, 0, len(grouped))
	for _, list := range grouped {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].depth != list[j].depth {
				return list[i].depth < list[j].depth
			}
			return list[i].RelativePath < list[j].RelativePath
		})
		result = append(result, list[0])
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].ModTime.Equal(result[j].ModTime) {
			return result[i].Name < result[j].Name
		}
		return result[i].ModTime.After(result[j].ModTime)
	})

	return result, nil
}

// resolveCredentialPath 通过凭据文件名解析真实路径：
// - 先尝试 authDir 根目录（兼容历史行为）；
// - 再从递归扫描结果中匹配同名文件。
func resolveCredentialPath(authDir, id string, pattern *regexp.Regexp) (string, error) {
	authDir = strings.TrimSpace(authDir)
	id = strings.TrimSpace(id)
	if authDir == "" || id == "" {
		return "", os.ErrNotExist
	}
	if pattern == nil || !pattern.MatchString(id) {
		return "", os.ErrNotExist
	}

	rootPath := filepath.Join(authDir, id)
	if info, err := os.Stat(rootPath); err == nil && !info.IsDir() {
		return rootPath, nil
	}

	files, err := collectCredentialFiles(authDir, pattern)
	if err != nil {
		return "", err
	}
	for _, file := range files {
		if file.Name == id {
			return file.Path, nil
		}
	}
	return "", os.ErrNotExist
}
