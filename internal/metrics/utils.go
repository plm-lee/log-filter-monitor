package metrics

import (
	"fmt"
	"sort"
	"strings"
)

// SortedTags 将标签 map 转换为排序后的字符串（参考 falcon-log-agent）
// tags: 标签 map
// 返回: 排序后的标签字符串，格式为 "key1=value1,key2=value2"
func SortedTags(tags map[string]string) string {
	if tags == nil || len(tags) == 0 {
		return ""
	}

	if len(tags) == 1 {
		for k, v := range tags {
			return fmt.Sprintf("%s=%s", k, v)
		}
	}

	// 获取所有 key 并排序
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 构建排序后的标签字符串
	ret := make([]string, len(keys))
	for i, key := range keys {
		ret[i] = fmt.Sprintf("%s=%s", key, tags[key])
	}

	return strings.Join(ret, ",")
}

// ParseTagString 解析标签字符串为 map（参考 falcon-log-agent）
// tagString: 标签字符串，格式为 "key1=value1,key2=value2"
// 返回: 标签 map
func ParseTagString(tagString string) map[string]string {
	if tagString == "" {
		return make(map[string]string)
	}

	tagString = strings.ReplaceAll(tagString, " ", "")
	tagDict := make(map[string]string)

	tags := strings.Split(tagString, ",")
	for _, tag := range tags {
		tagPair := strings.SplitN(tag, "=", 2)
		if len(tagPair) == 2 {
			tagDict[tagPair[0]] = tagPair[1]
		}
	}

	return tagDict
}

// AlignStepTms 将时间戳对齐到最近的步长（参考 falcon-log-agent）
// step: 步长（秒）
// tms: 时间戳（秒）
// 返回: 对齐后的时间戳
func AlignStepTms(step int64, tms int64) int64 {
	if step <= 0 {
		return tms
	}
	// 向前对齐到最近的 step 倍数
	return tms - (tms % step)
}

