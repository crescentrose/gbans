package cmd

import (
	"context"
	"encoding/json"
	"github.com/leighmacdonald/gbans/internal/config"
	"github.com/leighmacdonald/gbans/internal/model"
	"github.com/leighmacdonald/gbans/internal/store"
	"github.com/leighmacdonald/golib"
	"github.com/leighmacdonald/steamid/v2/steamid"
	"github.com/leighmacdonald/steamweb"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"io/ioutil"
	"time"
)

var testRconPass = "testing"
var seedFile = "seed.json"

type seedData struct {
	Admins  steamid.Collection `json:"admins"`
	Players steamid.Collection `json:"players"`
	Servers []struct {
		ShortName string `json:"short_name"`
		Host      string `json:"host"`
		Password  string `json:"password"`
	} `json:"servers"`
	Settings struct {
		Rcon string `json:"rcon"`
	}
}

// testDataCmd loads the db schema
var testDataCmd = &cobra.Command{
	Use:   "test_data",
	Short: "Add testing data",
	Run: func(cmd *cobra.Command, args []string) {
		db, err := store.New(config.DB.DSN)
		if err != nil {
			log.Fatalf("Failed to initialize db connection: %v", err)
		}
		if !golib.Exists(seedFile) {
			log.Fatalf("Seedfile does not exist: %s", seedFile)
		}
		sb, err := ioutil.ReadFile(seedFile)
		if err != nil {
			log.Fatalf("Failed to read seed file: %v", err)
		}
		var seed seedData
		if err := json.Unmarshal(sb, &seed); err != nil {
			log.Fatalf("failed to unmarshal seed file: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*20)
		defer cancel()

		for _, adminSid := range seed.Admins {
			var p model.Person
			_ = db.GetOrCreatePersonBySteamID(ctx, adminSid, &p)
			sum1, err := steamweb.PlayerSummaries(steamid.Collection{p.SteamID})
			if err != nil {
				log.Errorf("Failed to get player summary: %v", err)
				return
			}
			p.PermissionLevel = model.PAdmin
			p.PlayerSummary = &sum1[0]
			if err := db.SavePerson(ctx, &p); err != nil {
				log.Errorf("Failed to save person: %v", err)
				return
			}
		}
		for _, playerSid := range seed.Players {
			var p model.Person
			_ = db.GetOrCreatePersonBySteamID(ctx, playerSid, &p)
			sum1, err := steamweb.PlayerSummaries(steamid.Collection{p.SteamID})
			if err != nil {
				log.Errorf("Failed to get player summary: %v", err)
				return
			}
			p.PermissionLevel = model.PAuthenticated
			p.PlayerSummary = &sum1[0]
			if err := db.SavePerson(ctx, &p); err != nil {
				log.Errorf("Failed to save person: %v", err)
				return
			}
		}
		for _, server := range seed.Servers {
			pw := golib.RandomString(20)
			if server.Password != "" {
				pw = server.Password
			}
			rconPass := seed.Settings.Rcon
			if testRconPass != "" {
				rconPass = testRconPass
			}
			s := model.Server{
				ServerName:     server.ShortName,
				Address:        server.Host,
				Port:           27015,
				RCON:           rconPass,
				ReservedSlots:  8,
				Token:          "0123456789012345678901234567890123456789",
				Password:       pw,
				TokenCreatedOn: config.Now(),
				CreatedOn:      config.Now(),
				UpdatedOn:      config.Now(),
			}
			if err := db.SaveServer(ctx, &s); err != nil {
				log.Errorf("Failed to add server: %v", err)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(testDataCmd)

	testDataCmd.Flags().StringVarP(&testRconPass, "rcon", "r", "testing", "Sets the rcon password for test data")
	testDataCmd.Flags().StringVarP(&seedFile, "seed", "s", "seed.json", "Seed the database with this content")
}
