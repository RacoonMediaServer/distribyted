package torrent

import (
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/bep44"
	tlog "github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/storage"
	"github.com/rs/zerolog/log"

	"github.com/RacoonMediaServer/distribyted/config"
	dlog "github.com/RacoonMediaServer/distribyted/log"
)

func NewClient(st storage.ClientImpl, fis bep44.Store, cfg *config.TorrentGlobal, id [20]byte) (*torrent.Client, error) {
	// TODO download and upload limits
	torrentCfg := torrent.NewDefaultClientConfig()
	torrentCfg.Seed = true
	torrentCfg.PeerID = string(id[:])
	torrentCfg.DefaultStorage = st
	torrentCfg.DisableIPv6 = cfg.DisableIPv6

	l := log.Logger.With().Str("component", "torrent-client").Logger()

	tl := tlog.NewLogger()
	tl.SetHandlers(&dlog.Torrent{L: l})
	torrentCfg.Logger = tl

	torrentCfg.ConfigureAnacrolixDhtServer = func(cfg *dht.ServerConfig) {
		cfg.Store = fis
		cfg.Exp = 2 * time.Hour
		cfg.NoSecurity = false
	}

	return torrent.NewClient(torrentCfg)
}
