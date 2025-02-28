package app

import (
	"github.com/leighmacdonald/gbans/internal/event"
	"github.com/leighmacdonald/gbans/internal/model"
	"github.com/leighmacdonald/gbans/pkg/logparse"
	log "github.com/sirupsen/logrus"
	"sync"
)

var (
	wordFilters   []model.Filter
	wordFiltersMu *sync.RWMutex
)

func init() {
	wordFiltersMu = &sync.RWMutex{}
}

// importFilteredWords loads the supplied word list into memory
func importFilteredWords(filters []model.Filter) {
	wordFiltersMu.Lock()
	defer wordFiltersMu.Unlock()
	wordFilters = filters
}

func (g *gbans) filterWorker() {
	c := make(chan model.ServerEvent)
	if err := event.RegisterConsumer(c, []logparse.MsgType{logparse.Say, logparse.SayTeam}); err != nil {
		log.Fatalf("Failed to register event reader: %v", err)
	}
	for {
		select {
		case evt := <-c:
			matched, _ := g.ContainsFilteredWord(evt.Extra)
			if matched {
				g.addWarning(evt.Source.SteamID, warnLanguage)
			}
		case <-g.ctx.Done():
			return
		}
	}
}
