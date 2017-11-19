package bot

import (
	log "github.com/Sirupsen/logrus"
	"github.com/jonas747/dbstate"
	"github.com/jonas747/discordgo"
	"github.com/jonas747/dshardmanager"
	"github.com/jonas747/yagpdb/bot/eventsystem"
	"github.com/jonas747/yagpdb/common"
	"os"
	"strconv"
	"sync"
	"time"
)

var (
	// When the bot was started
	Started      = time.Now()
	Running      bool
	State        *dbstate.State
	ShardManager *dshardmanager.Manager

	StateHandlerPtr *eventsystem.Handler
)

func Setup() {

	// Things may rely on state being available at this point for initialization
	eventsystem.AddHandler(HandleReady, eventsystem.EventReady)
	StateHandlerPtr = eventsystem.AddHandler(StateHandler, eventsystem.EventAll)
	eventsystem.AddHandler(HandlePresenceUpdate, eventsystem.EventPresenceUpdate)
	eventsystem.AddHandler(ConcurrentEventHandler(EventLogger.handleEvent), eventsystem.EventAll)

	eventsystem.AddHandler(RedisWrapper(HandleGuildCreate), eventsystem.EventGuildCreate)
	eventsystem.AddHandler(RedisWrapper(HandleGuildDelete), eventsystem.EventGuildDelete)

	eventsystem.AddHandler(RedisWrapper(HandleGuildUpdate), eventsystem.EventGuildUpdate)
	eventsystem.AddHandler(RedisWrapper(HandleGuildRoleCreate), eventsystem.EventGuildRoleCreate)
	eventsystem.AddHandler(RedisWrapper(HandleGuildRoleUpdate), eventsystem.EventGuildRoleUpdate)
	eventsystem.AddHandler(RedisWrapper(HandleGuildRoleRemove), eventsystem.EventGuildRoleDelete)
	eventsystem.AddHandler(RedisWrapper(HandleChannelCreate), eventsystem.EventChannelCreate)
	eventsystem.AddHandler(RedisWrapper(HandleChannelUpdate), eventsystem.EventChannelUpdate)
	eventsystem.AddHandler(RedisWrapper(HandleChannelDelete), eventsystem.EventChannelDelete)
	eventsystem.AddHandler(RedisWrapper(HandleGuildMemberUpdate), eventsystem.EventGuildMemberUpdate)

	log.Info("Initializing bot plugins")
	for _, plugin := range common.Plugins {
		if botPlugin, ok := plugin.(Plugin); ok {
			botPlugin.InitBot()
			log.Info("Initialized bot plugin ", plugin.Name())
		}
	}

	log.Printf("Registered %d event handlers", eventsystem.NumHandlers(eventsystem.EventAll))
}

func Run() {

	log.Println("Running bot")

	// Set up shard manager
	ShardManager = dshardmanager.New(common.Conf.BotToken)
	ShardManager.LogChannel = os.Getenv("YAGPDB_CONNEVT_CHANNEL")
	ShardManager.StatusMessageChannel = os.Getenv("YAGPDB_CONNSTATUS_CHANNEL")
	ShardManager.Name = "YAGPDB"
	ShardManager.GuildCountsFunc = GuildCountsFunc
	ShardManager.SessionFunc = func(token string) (session *discordgo.Session, err error) {
		session, err = discordgo.New(token)
		if err != nil {
			return
		}

		session.StateEnabled = false
		session.LogLevel = discordgo.LogInformational
		session.SyncEvents = true

		return
	}
	// Only handler
	ShardManager.AddHandler(eventsystem.HandleEvent)

	bOpts := dbstate.RecommendedBadgerOptions("state")

	numShard, err := ShardManager.GetRecommendedCount()
	if err != nil {
		panic("failed retrieving recommended shards")
	}

	State, err = dbstate.NewState(numShard, dbstate.Options{
		DBOpts:              bOpts,
		KeepDeletedMessages: true,
		MessageTTL:          time.Hour * 6,
		TrackChannels:       true,
		TrackMembers:        true,
		TrackMessages:       true,
		TrackPresences:      true,
		TrackRoles:          true,
		UseChannelSyncMode:  false,
	})

	if err != nil {
		panic("failed initalizing state: " + err.Error())
	}

	err = loadRedisConnectedGuilds()
	if err != nil {
		panic("failed loading connnected guilds: " + err.Error())
	}

	Running = true
	go ShardManager.Start()

	go mergedMessageSender()
	go MemberFetcher.Run()

	for _, p := range common.Plugins {
		starter, ok := p.(BotStarterHandler)
		if ok {
			starter.StartBot()
			log.Debug("Ran StartBot for ", p.Name())
		}
	}

	go checkConnectedGuilds()
}

func Stop(wg *sync.WaitGroup) {

	for _, v := range common.Plugins {
		stopper, ok := v.(BotStopperHandler)
		if !ok {
			continue
		}
		wg.Add(1)
		log.Debug("Sending stop event to stopper: ", v.Name())
		go stopper.StopBot(wg)
	}

	ShardManager.StopAll()
	State.Close()
	wg.Done()
}

func loadRedisConnectedGuilds() error {
	c := common.MustGetRedisClient()
	l, err := c.Cmd("SMEMBERS", "connected_guilds").List()
	if err != nil {
		return err
	}

	redisConnectedGuilds = make([]int64, 0, len(l))

	for _, v := range l {
		parsed := common.MustParseInt(v)
		redisConnectedGuilds = append(redisConnectedGuilds, parsed)
	}

	return nil
}

var redisConnectedGuilds []int

// checks all connected guilds and emites guildremoved on those no longer connected
func checkConnectedGuilds(shard int, numShards int, guilds []*discordgo.Guild) {
	log.Info("Checking joined guilds")

	redisClient := common.MustGetRedisClient()

OUTER:
	for _, rg := range redisConnectedGuilds {
		rgShard := (rg >> 22) % numShards
		if rgShard == shard {
			for _, g := range guilds {
				if common.MustParseInt(g.ID) == rg {
					continue OUTER
				}
			}
		}

		redisClient.Cmd("SREM", "connected_guilds", rg)

		// Was not found if we got here, meaning we left the guild while offline
		EmitGuildRemoved(client, gID)
		log.WithField("guild", gID).Info("Removed from guild when offline")
	}
}

func GuildCountsFunc() []int {
	numShards := ShardManager.GetNumShards()
	result := make([]int, numShards)

	State.IterateGuilds(nil, func(g *discordgo.Guild) bool {

		parsed, _ := strconv.ParseInt(g.ID, 10, 64)
		result[(parsed>>22)%int64(numShards)]++

		return true
	})

	return result
}
