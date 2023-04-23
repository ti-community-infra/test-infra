package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

const (
	defaultSystemMessage          = "You are an experienced software developer. You will act as a reviewer for a GitHub Pull Request, and you should answer by markdown format."
	defaultPromte                 = "Please help me to review the github pull request: summarize the key changes and identify potential problems, then give some fixing suggestions, all you output should be markdown."
	defaultPrPatchIntroducePromte = "This is the diff for the pull request:"
	defaultStaticOutHeadnote      = `> **I have already done a preliminary review for you, and I hope to help you do a better job.**
------
`
)

// Config represent the plugin configuration
//
// layer: org|repo / task / task-config
type Config map[string]map[string]TaskConfig

// TaskConfig reprensent the config for AI task item.
//
// $SystemMessage
// --------------
// < $Prompt
// < Here are the serval context contents:
// $ExternalContexts.each do
//
//	< - format(it.PromptTpl, fetch(it.ResURL))
//
// < $PatchIntroducerPrompt
// < ```diff
// < diff content
// < ```
// ~~~~~~~~~~~~~~~~~~~~~~~~~~~~
// > <OutputStaticHeadNote>
// > responses from AI server.
//
// TODO(wuhuizuo): using go template to comose the question.
type TaskConfig struct {
	Name                 string             `yaml:"name,omitempty" json:"name,omitempty"`
	SystemMessage        string             `yaml:"system_message,omitempty" json:"system_message,omitempty"`
	UserPrompt           string             `yaml:"user_prompt,omitempty" json:"user_prompt,omitempty"`
	PatchIntroducePrompt string             `yaml:"patch_introduce_prompt,omitempty" json:"patch_introduce_prompt,omitempty"`
	OutputStaticHeadNote string             `yaml:"output_static_head_note,omitempty" json:"output_static_head_note,omitempty"`
	ExternalContexts     []*ExternalContext `yaml:"external_contexts,omitempty" json:"external_contexts,omitempty"`
}

type ExternalContext struct {
	PromptTpl  string `yaml:"prompt_tpl,omitempty" json:"prompt_tpl,omitempty"`
	ResURL     string `yaml:"res_url,omitempty" json:"res_url,omitempty"`
	resContent []byte
}

type ConfigAgent struct {
	path   string
	config Config
	mu     sync.RWMutex
}

func (ec *ExternalContext) Content() ([]byte, error) {
	if len(ec.resContent) == 0 {
		// TODO(wuhuizuo): fetch content from `ec.ResURL` and fill `ec.resContent`, maybe we need RW lock.
	}

	return ec.resContent, nil
}

// NewConfigAgent returns a new ConfigLoader.
func NewConfigAgent(path string, watchInterval time.Duration) (*ConfigAgent, error) {
	c := &ConfigAgent{path: path}
	err := c.Reload(path)
	if err != nil {
		return nil, err
	}

	go c.WatchConfig(context.Background(), watchInterval, c.Reload)

	return c, nil
}

// WatchConfig monitors a file for changes and sends a message on the channel when the file changes
func (c *ConfigAgent) WatchConfig(ctx context.Context, interval time.Duration, onChangeHandler func(f string) error) {
	var lastMod time.Time
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(c.path)
			if err != nil {
				fmt.Printf("Error getting file info: %v\n", err)
			} else if modTime := info.ModTime(); modTime.After(lastMod) {
				lastMod = modTime
				onChangeHandler(c.path)
			}
		}
	}
}

// Reload read and update config data.
func (c *ConfigAgent) Reload(file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("could no load config file %s: %w", file, err)
	}

	config := Config{}
	err = json.Unmarshal(data, &config)
	if err != nil {
		return fmt.Errorf("could not unmarshal JSON config: %w", err)
	}

	// Set config.
	c.mu.Lock()
	c.config = config
	c.mu.Unlock()

	return nil
}

// Get return the config data.
func (c *ConfigAgent) TasksFor(org, repo string) (map[string]TaskConfig, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	fullName := fmt.Sprintf("%s/%s", org, repo)
	repoTasks, ok := c.config[fullName]
	if ok {
		return repoTasks, nil
	}

	orgTasks, ok := c.config[repo]
	if ok {
		return orgTasks, nil
	}

	return map[string]TaskConfig{
		"default": TaskConfig{},
	}, nil
}
