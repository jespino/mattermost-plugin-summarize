package ambiance

import (
	"time"

	"github.com/go-co-op/gocron"
	"github.com/mattermost/mattermost-plugin-ai/server/ai"
)

type Ambiance struct {
	llm       ai.LanguageModel
	scheduler *gocron.Scheduler
}

func New(llm ai.LanguageModel) *Ambiance {
	s := gocron.NewScheduler(time.UTC)
	return &Ambiance{
		llm:       llm,
		scheduler: s,
	}
}

func (a *Ambiance) ChannelsSummary() {
}

func (a *Ambiance) Start() {
	a.scheduler.Every(1).Day().At("00:00").Do(a.ChannelsSummary)
	a.scheduler.StartAsync()
}

func (a *Ambiance) Stop() {
	a.scheduler.Clear()
	a.scheduler.Stop()
}
