// Copyright (c) 2022-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/server/telemetry"
)

const (
	// server-side events
	evUserStartedConversation = "user_started_conversation"
	evContextualInterrogation = "contextual_interrogation"
	evAIBotMention            = "ai_bot_mention"
	evSummarizeUnreadMessages = "summarize_unread_messages"
	evSummarizeThread         = "summarize_thread"
	evSummarizeTranscription  = "summarize_transcription"
)

var (
	telemetryClientTypes  = []string{"web", "mobile", "desktop"}
	telemetryClientEvents = []string{
		"copilot_apps_bar_clicked",
	}
	telemetryClientTypesMap  map[string]struct{}
	telemetryClientEventsMap map[string]struct{}
)

func init() {
	telemetryClientEventsMap = make(map[string]struct{}, len(telemetryClientEvents))
	for _, eventType := range telemetryClientEvents {
		telemetryClientEventsMap[eventType] = struct{}{}
	}
	telemetryClientTypesMap = make(map[string]struct{}, len(telemetryClientTypes))
	for _, clientType := range telemetryClientTypes {
		telemetryClientTypesMap[clientType] = struct{}{}
	}
}

type trackEventRequest struct {
	Event      string                 `json:"event"`
	ClientType string                 `json:"clientType"`
	Source     string                 `json:"source"`
	Props      map[string]interface{} `json:"props"`
}

func (p *Plugin) track(ev string, props map[string]interface{}) {
	p.telemetryMut.RLock()
	defer p.telemetryMut.RUnlock()
	if p.telemetry == nil {
		return
	}
	if err := p.telemetry.Track(ev, props); err != nil {
		p.API.LogError(err.Error())
	}
}

func (p *Plugin) uninitTelemetry() error {
	p.telemetryMut.Lock()
	defer p.telemetryMut.Unlock()
	if p.telemetry == nil {
		return nil
	}
	return p.telemetry.Close()
}

func (p *Plugin) initTelemetry(enableDiagnostics *bool) error {
	p.telemetryMut.Lock()
	defer p.telemetryMut.Unlock()
	if p.telemetry == nil && enableDiagnostics != nil && *enableDiagnostics {
		p.API.LogDebug("Initializing telemetry")
		// setup telemetry
		client, err := telemetry.NewClient(telemetry.ClientConfig{
			WriteKey:     rudderWriteKey,
			DataplaneURL: rudderDataplaneURL,
			DiagnosticID: p.API.GetDiagnosticId(),
			DefaultProps: map[string]interface{}{
				"ServerVersion": p.API.GetServerVersion(),
				"PluginVersion": manifest.Version,
				"PluginBuild":   buildHash,
			},
		})
		if err != nil {
			return err
		}
		p.telemetry = client
	} else if p.telemetry != nil && (enableDiagnostics == nil || !*enableDiagnostics) {
		p.API.LogDebug("Deinitializing telemetry")
		// destroy telemetry
		if err := p.telemetry.Close(); err != nil {
			return err
		}
		p.telemetry = nil
	}
	return nil
}

func (p *Plugin) handleTrackEvent(c *gin.Context) {
	p.telemetryMut.RLock()
	telemetryEnabled := p.telemetry != nil
	p.telemetryMut.RUnlock()

	if !telemetryEnabled {
		return
	}

	var data trackEventRequest
	if err := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, requestBodyMaxSizeBytes)).Decode(&data); err != nil {
		return
	}

	if _, ok := telemetryClientEventsMap[data.Event]; !ok {
		return
	}

	if _, ok := telemetryClientTypesMap[data.ClientType]; !ok {
		return
	}

	if data.Props == nil {
		data.Props = map[string]interface{}{}
	}

	if data.Source != "" {
		data.Props["Source"] = data.Source
	}

	data.Props["ActualUserID"] = c.GetHeader("Mattermost-User-Id")
	data.Props["ClientType"] = data.ClientType

	p.track(data.Event, data.Props)
}