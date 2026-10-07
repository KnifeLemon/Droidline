package store

import (
	"os"
	"strings"
)

func langFromEnv() string {
	for _, k := range []string{"DROIDLINE_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return langCode(v)
		}
	}
	return "en"
}

// langCode maps a locale such as ko-KR or zh_CN.UTF-8 to en, ko or zh.
func langCode(locale string) string {
	l := strings.ToLower(locale)
	switch {
	case strings.HasPrefix(l, "ko"):
		return "ko"
	case strings.HasPrefix(l, "zh"):
		return "zh"
	}
	return "en"
}
