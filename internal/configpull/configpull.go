package configpull

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"log-filter-monitor/internal/filter"
	"gopkg.in/yaml.v2"
)

// RulesOnly 从 Manager 拉取的配置中仅解析 rules 部分
type RulesOnly struct {
	Rules []filter.Rule `yaml:"rules"`
}

// Fetcher 配置拉取器
type Fetcher struct {
	url      string
	agentID  string
	apiKey   string
	interval time.Duration
	client   *http.Client
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// NewFetcher 创建配置拉取器
func NewFetcher(pullURL, agentID, apiKey string, interval time.Duration) *Fetcher {
	if agentID == "" {
		agentID = "default"
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Fetcher{
		url:      pullURL,
		agentID:  agentID,
		apiKey:   apiKey,
		interval: interval,
		client:   &http.Client{Timeout: 10 * time.Second},
		stopChan: make(chan struct{}),
	}
}

// Fetch 拉取配置，返回 YAML 原文
func (f *Fetcher) Fetch() ([]byte, error) {
	u, err := url.Parse(f.url)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("agent_id", f.agentID)
	u.RawQuery = q.Encode()
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	if f.apiKey != "" {
		req.Header.Set("X-API-Key", f.apiKey)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return readAll(resp)
}

func readAll(r *http.Response) ([]byte, error) {
	return io.ReadAll(r.Body)
}

// ParseRules 解析 YAML 中的 rules
func ParseRules(yamlData []byte) ([]filter.Rule, error) {
	var parsed RulesOnly
	if err := yaml.Unmarshal(yamlData, &parsed); err != nil {
		return nil, err
	}
	return parsed.Rules, nil
}

// OnRules 规则更新回调
type OnRules func(rules []filter.Rule) error

// Start 启动定时拉取
func (f *Fetcher) Start(onRules OnRules) {
	if onRules == nil {
		return
	}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		ticker := time.NewTicker(f.interval)
		defer ticker.Stop()
		for {
			select {
			case <-f.stopChan:
				return
			case <-ticker.C:
				data, err := f.Fetch()
				if err != nil {
					log.Printf("配置拉取失败: %v\n", err)
					continue
				}
				rules, err := ParseRules(data)
				if err != nil {
					log.Printf("配置解析失败: %v\n", err)
					continue
				}
				if len(rules) == 0 {
					continue
				}
				if err := onRules(rules); err != nil {
					log.Printf("配置热更新失败: %v\n", err)
					continue
				}
				log.Printf("配置热更新成功，规则数: %d\n", len(rules))
			}
		}
	}()
}

// Stop 停止拉取
func (f *Fetcher) Stop() {
	close(f.stopChan)
	f.wg.Wait()
}
