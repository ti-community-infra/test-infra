/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pluginhelp/externalplugins"
)

type options struct {
	port int

	openaiConfigFile         string
	openaiModel              string
	opeaiTasksFile           string
	opeaiTasksReloadInterval time.Duration

	dryRun                 bool
	github                 prowflagutil.GitHubOptions
	instrumentationOptions prowflagutil.InstrumentationOptions
	logLevel               string

	webhookSecretFile string
}

type openaiConfig struct {
	Token      string `yaml:"token,omitempty" json:"token,omitempty"`
	BaseURL    string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	OrgID      string `yaml:"org_id,omitempty" json:"org_id,omitempty"`
	APIType    string `yaml:"api_type,omitempty" json:"api_type,omitempty"`       // OPEN_AI | AZURE | AZURE_AD
	APIVersion string `yaml:"api_version,omitempty" json:"api_version,omitempty"` // 2023-03-15-preview, required when APIType is APITypeAzure or APITypeAzureAD
	Engine     string `yaml:"engine,omitempty" json:"engine,omitempty"`           // required when APIType is APITypeAzure or APITypeAzureAD, it's the deploy instance name.
}

func (o *options) Validate() error {
	for idx, group := range []flagutil.OptionGroup{&o.github} {
		if err := group.Validate(o.dryRun); err != nil {
			return fmt.Errorf("%d: %w", idx, err)
		}
	}

	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.IntVar(&o.port, "port", 8888, "Port to listen on.")
	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.openaiConfigFile, "openai-config-file", "/etc/openai/config.yaml", "Path to the file containing the ChatGPT api token.")
	fs.StringVar(&o.opeaiTasksFile, "openai-tasks-file", "/etc/openai/tasks.yaml", "Path to the file containing the default openai tasks.")
	fs.DurationVar(&o.opeaiTasksReloadInterval, "openai-tasks-reload-interval", time.Minute, "Interval to reload the openai tasks file.")
	fs.StringVar(&o.openaiModel, "openai-model", openai.GPT3Dot5Turbo, "OpenAI model, list ref: https://github.com/sashabaranov/go-openai/blob/master/completion.go#L15-L38")
	fs.StringVar(&o.logLevel, "log-level", "debug", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	for _, group := range []flagutil.OptionGroup{&o.github, &o.instrumentationOptions} {
		group.AddFlags(fs)
	}
	fs.Parse(os.Args[1:])
	return o
}

func newOpenAIClient(yamlCfgFile string) (*openai.Client, error) {
	// Read the contents of the file into a byte slice
	content, err := ioutil.ReadFile(yamlCfgFile)
	if err != nil {
		return nil, fmt.Errorf("Error reading file: %w", err)
	}

	// Unmarshal the YAML data into a Config struct
	var cfg openaiConfig
	err = yaml.Unmarshal(content, &cfg)
	if err != nil {
		return nil, fmt.Errorf("Error unmarshaling YAML: %w", err)
	}

	openaiCfg := openai.DefaultConfig(cfg.Token)
	openaiCfg.BaseURL = cfg.BaseURL
	openaiCfg.OrgID = cfg.OrgID
	openaiCfg.APIType = openai.APIType(cfg.APIType)
	openaiCfg.APIVersion = cfg.APIVersion
	openaiCfg.Engine = cfg.Engine

	return openai.NewClientWithConfig(openaiCfg), nil
}

func main() {
	logrusutil.ComponentInit()
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	logLevel, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to parse loglevel")
	}
	logrus.SetLevel(logLevel)
	log := logrus.StandardLogger().WithField("plugin", pluginName)

	if err := secret.Add(o.webhookSecretFile); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	githubClient, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}

	openaiClient, err := newOpenAIClient(o.openaiConfigFile)
	if err != nil {
		logrus.WithError(err).Fatal("Error create OpenAI client.")
	}

	taskAgent, err := NewConfigAgent(o.opeaiTasksFile, o.opeaiTasksReloadInterval)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to start task agent")
	}

	server := &Server{
		tokenGenerator:  secret.GetTokenGenerator(o.webhookSecretFile),
		ghc:             githubClient,
		openaiClient:    openaiClient,
		openaiModel:     o.openaiModel,
		openaiTaskAgent: taskAgent,
		log:             log,
	}

	health := pjutil.NewHealthOnPort(o.instrumentationOptions.HealthPort)
	health.ServeReady()

	mux := http.NewServeMux()
	mux.Handle("/", server)
	externalplugins.ServeExternalPluginHelp(mux, log, HelpProvider)
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: mux}
	defer interrupts.WaitForGracefulShutdown()
	interrupts.ListenAndServe(httpServer, 5*time.Second)
}
