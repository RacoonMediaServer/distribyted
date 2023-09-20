package torrent

import (
	"bytes"
	"errors"
	"path"
	"sync"
	"time"

	"github.com/RacoonMediaServer/distribyted/fs"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/distribyted/distribyted/fs"
	"github.com/distribyted/distribyted/torrent/loader"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Service struct {
	c *torrent.Client

	s *Stats

	mu  sync.Mutex
	fss map[string]fs.Filesystem

	loaders []DatabaseLoader

	log                     zerolog.Logger
	addTimeout, readTimeout int
}

type DatabaseLoader interface {
	ListTorrents() (map[string][][]byte, error)
}

func NewService(loaders []DatabaseLoader, stats *Stats, c *torrent.Client, addTimeout, readTimeout int) *Service {
	l := log.Logger.With().Str("component", "torrent-service").Logger()
	return &Service{
		log:         l,
		s:           stats,
		c:           c,
		fss:         make(map[string]fs.Filesystem),
		loaders:     loaders,
		addTimeout:  addTimeout,
		readTimeout: readTimeout,
	}
}

func isMagnetLink(data []byte) bool {
	const magnetLinkSign = "magnet:"
	if len(data) < len(magnetLinkSign) {
		return false
	}
	return string(data[:len(magnetLinkSign)]) == magnetLinkSign
}

func (s *Service) Load() (map[string]fs.Filesystem, error) {
	// Load from config
	s.log.Info().Msg("adding torrents from sources")
	for _, loader := range s.loaders {
		if err := s.load(loader); err != nil {
			return nil, err
		}
	}
	return s.fss, nil
}

func (s *Service) load(l DatabaseLoader) error {
	list, err := l.ListTorrents()
	if err != nil {
		return err
	}
	for r, ms := range list {
		s.addRoute(r)
		for _, m := range ms {
			if _, err := s.add(r, m); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *Service) Add(r string, content []byte) (string, error) {
	return s.add(r, content)
}

func (s *Service) add(r string, content []byte) (string, error) {
	var spec *torrent.TorrentSpec
	isMagnet := isMagnetLink(content)
	if !isMagnet {
		mi, err := metainfo.Load(bytes.NewReader(content))
		if err != nil {
			return "", err
		}
		spec = torrent.TorrentSpecFromMetaInfo(mi)
	} else {
		var err error
		spec, err = torrent.TorrentSpecFromMagnetUri(string(content))
		if err != nil {
			return "", err
		}
	}

	opts := torrent.AddTorrentOpts{
		InfoHash:  spec.InfoHash,
		ChunkSize: spec.ChunkSize,
	}

	t, _ := s.c.AddTorrentOpt(opts)
	if err := t.MergeSpec(spec); err != nil {
		t.Drop()
		return "", err
	}

	return s.addTorrent(r, t)

}

func (s *Service) addRoute(r string) {
	s.s.AddRoute(r)

	// Add to filesystems
	folder := path.Join("/", r)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.fss[folder]
	if !ok {
		s.fss[folder] = fs.NewTorrent(s.readTimeout)
	}
}

func (s *Service) addTorrent(r string, t *torrent.Torrent) (string, error) {
	// only get info if name is not available
	if t.Info() == nil {
		s.log.Info().Str("hash", t.InfoHash().String()).Msg("getting torrent info")
		select {
		case <-time.After(time.Duration(s.addTimeout) * time.Second):
			s.log.Error().Str("hash", t.InfoHash().String()).Msg("timeout getting torrent info")
			return "", errors.New("timeout getting torrent info")
		case <-t.GotInfo():
			s.log.Info().Str("hash", t.InfoHash().String()).Msg("obtained torrent info")
		}

	}

	// Add to stats
	s.s.Add(r, t)

	// Add to filesystems
	folder := path.Join("/", r)
	s.mu.Lock()
	defer s.mu.Unlock()

	tfs, ok := s.fss[folder].(*fs.Torrent)
	if !ok {
		return "", errors.New("error adding torrent to filesystem")
	}

	tfs.AddTorrent(t)
	s.log.Info().Str("name", t.Info().Name).Str("route", r).Msg("torrent added")

	return r, nil
}

func (s *Service) RemoveFromHash(r, h string) error {
	// Remove from stats
	s.s.Del(r, h)

	// Remove from fs
	folder := path.Join("/", r)

	tfs, ok := s.fss[folder].(*fs.Torrent)
	if !ok {
		return errors.New("error removing torrent from filesystem")
	}

	tfs.RemoveTorrent(h)

	// Remove from client
	var mh metainfo.Hash
	if err := mh.FromHexString(h); err != nil {
		return err
	}

	t, ok := s.c.Torrent(metainfo.NewHashFromHex(h))
	if ok {
		t.Drop()
	}

	return nil
}
