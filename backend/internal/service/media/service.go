package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// ReasonNoServer is why the gateway is off when no GPU server is set.
const ReasonNoServer = "no_server"

// Polling the GPU server while it works.
const (
	defaultPollEvery = 2 * time.Second
	maxPollMisses    = 5
	statusTimeout    = 8 * time.Second
	defaultTickEvery = 20 * time.Second
	defaultWakeEvery = 10 * time.Second
	wakeTimeout      = 10 * time.Minute
)

// Config sets the gateway up. With no BaseURL the library still lists,
// keeps uploads and deletes, but nothing new can be made.
type Config struct {
	LibraryDir string
	BaseURL    string
	Token      string
	// Reason says why there is no server (from the settings), if not set.
	Reason string
	// Voice is reported to the app: X-00 speaks through the server.
	Voice     bool
	Client    *http.Client
	Logger    *slog.Logger
	PollEvery time.Duration
	Now       func() time.Time
	// PricePerHour is what the person pays for the server, for the meter.
	PricePerHour float64
	// Power switches the server on for work and, after IdleMinutes with
	// nothing to do, off. Nil leaves it to the person.
	Power       PowerSwitch
	IdleMinutes int
	TickEvery   time.Duration
	WakeEvery   time.Duration
}

// Service is the media gateway.
type Service struct {
	lib       *library
	server    *server
	reason    string
	voice     bool
	log       *slog.Logger
	now       func() time.Time
	pollEvery time.Duration
	price     float64
	power     PowerSwitch
	idle      time.Duration
	tickEvery time.Duration
	wakeEvery time.Duration
	lastBusy  atomic.Int64
	usageMu   sync.Mutex
	wakeMu    sync.Mutex
	promoteMu sync.Mutex

	queue   chan string
	mu      sync.Mutex
	pending int
	cancels map[string]context.CancelFunc
	ctx     context.Context
	stop    context.CancelFunc
	wg      sync.WaitGroup
}

// New opens the library and, when a server is set, starts the workers.
// Work left unfinished by the last run of the app is marked interrupted.
func New(cfg Config) *Service {
	s := &Service{
		lib:       &library{root: cfg.LibraryDir},
		reason:    cfg.Reason,
		voice:     cfg.Voice,
		log:       cfg.Logger,
		now:       cfg.Now,
		pollEvery: cfg.PollEvery,
		queue:     make(chan string, maxQueued),
		cancels:   make(map[string]context.CancelFunc),
		price:     cfg.PricePerHour,
		power:     cfg.Power,
		idle:      time.Duration(cfg.IdleMinutes) * time.Minute,
		tickEvery: cfg.TickEvery,
		wakeEvery: cfg.WakeEvery,
	}
	if s.tickEvery <= 0 {
		s.tickEvery = defaultTickEvery
	}
	if s.wakeEvery <= 0 {
		s.wakeEvery = defaultWakeEvery
	}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.pollEvery <= 0 {
		s.pollEvery = defaultPollEvery
	}
	s.ctx, s.stop = context.WithCancel(context.Background())
	if err := s.lib.ensure(); err != nil {
		s.log.Warn("media: the library folder could not be made", "error", err)
	}
	s.interruptLeftovers()
	if strings.TrimSpace(cfg.BaseURL) == "" {
		if s.reason == "" {
			s.reason = ReasonNoServer
		}
		return s
	}
	client := cfg.Client
	if client == nil {
		client = NewHTTPClient()
	}
	s.server = &server{base: strings.TrimRight(cfg.BaseURL, "/"), token: cfg.Token, client: client}
	s.reason = ""
	for i := 0; i < maxRunning; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	s.lastBusy.Store(s.now().UnixNano())
	s.wg.Add(1)
	go s.ticker()
	return s
}

func (s *Service) ticker() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.tickEvery)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// Close stops the workers; unfinished work is marked interrupted next time.
func (s *Service) Close() {
	s.stop()
	s.wg.Wait()
}

// Info says whether the gateway can make anything, and what.
func (s *Service) Info() Info {
	return Info{
		Enabled:      s.server != nil,
		Reason:       s.reason,
		Voice:        s.voice && s.server != nil,
		Capabilities: s.capabilities(),
		Root:         s.lib.root,
	}
}

// Status asks the GPU server how it is, with the meter and the night queue.
// An unreachable server is not an error.
func (s *Service) Status(ctx context.Context) ServerStatus {
	if s.server == nil {
		return ServerStatus{}
	}
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	status, err := s.server.stats(ctx)
	if err != nil {
		status = ServerStatus{}
	}
	now := s.now()
	status.OnSecondsToday, status.OnSecondsMonth = s.onTime(now)
	if s.price > 0 {
		status.PricePerHour = s.price
		status.CostToday = cost(status.OnSecondsToday, s.price)
		status.CostMonth = cost(status.OnSecondsMonth, s.price)
	}
	if s.power != nil {
		status.AutoPower = true
		status.IdleMinutes = int(s.idle / time.Minute)
	}
	if items, err := s.lib.list(); err == nil {
		for _, item := range items {
			if item.State == StateScheduled {
				status.Scheduled++
			}
		}
	}
	return status
}

// List returns the library, newest first.
func (s *Service) List() ([]Item, error) {
	items, err := s.lib.list()
	if err != nil {
		return nil, err
	}
	if len(items) > MaxListed {
		items = items[:MaxListed]
	}
	return items, nil
}

// Get returns one item.
func (s *Service) Get(id string) (Item, error) { return s.lib.get(id) }

// Create queues one request and returns it at once; the work goes on in the
// background and the item changes as it does.
func (s *Service) Create(request Request) (Item, error) {
	if s.server == nil {
		return Item{}, ErrNotConfigured
	}
	file, ok := s.workflows()[strings.TrimSpace(request.Capability)]
	if !ok {
		return Item{}, ErrUnknownCapability
	}
	item, err := s.newItem(request, file)
	if err != nil {
		return Item{}, err
	}
	count := request.Count
	if count == 0 {
		count = 1
	}
	now := s.now()
	if count < 1 || count > MaxBatch || (request.RunAt != nil && request.RunAt.After(now.Add(maxAhead))) {
		return Item{}, ErrInvalidRequest
	}
	if count > 1 || (request.RunAt != nil && request.RunAt.After(now)) {
		return s.schedule(request, file, item, count)
	}
	s.mu.Lock()
	if s.pending >= maxQueued {
		s.mu.Unlock()
		return Item{}, ErrBusy
	}
	s.pending++
	s.mu.Unlock()
	if err := s.lib.save(item); err != nil {
		s.done()
		return Item{}, err
	}
	// The queue holds maxQueued and pending never exceeds it, so this never waits.
	s.queue <- item.ID
	return item, nil
}

// schedule keeps a batch, or work for later, as scheduled items; the ticker
// sends each to the queue when its time comes and there is room.
func (s *Service) schedule(request Request, file workflowFile, first Item, count int) (Item, error) {
	waiting := 0
	if items, err := s.lib.list(); err == nil {
		for _, item := range items {
			if item.State == StateScheduled {
				waiting++
			}
		}
	}
	if waiting+count > maxScheduled {
		return Item{}, ErrBusy
	}
	when := s.now().UTC()
	if request.RunAt != nil && request.RunAt.After(when) {
		when = request.RunAt.UTC()
	}
	for i := 0; i < count; i++ {
		item := first
		if i > 0 {
			next, err := s.newItem(request, file)
			if err != nil {
				return Item{}, err
			}
			item = next
			if request.Seed > 0 {
				item.Seed = request.Seed + int64(i)
			}
		}
		at := when
		item.RunAt = &at
		item.State = StateScheduled
		if err := s.lib.save(item); err != nil {
			return Item{}, err
		}
		if i == 0 {
			first = item
		}
	}
	go s.promote()
	return first, nil
}

// newItem checks a request against its capability and fills the defaults.
func (s *Service) newItem(request Request, file workflowFile) (Item, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" || utf8.RuneCountInString(prompt) > MaxPromptRunes {
		return Item{}, ErrInvalidRequest
	}
	negative := strings.TrimSpace(request.Negative)
	if utf8.RuneCountInString(negative) > MaxNegativeRunes {
		return Item{}, ErrInvalidRequest
	}
	if negative == "" {
		negative = file.Defaults.Negative
	}
	width, height, seconds, err := sizes(file, request)
	if err != nil {
		return Item{}, err
	}
	input := ""
	if usesPlaceholder(file.Workflow, "{{image}}") {
		if _, _, err := s.inputPicture(strings.TrimSpace(request.Input)); err != nil {
			return Item{}, ErrInvalidRequest
		}
		input = strings.TrimSpace(request.Input)
	}
	seed := request.Seed
	if seed <= 0 {
		seed = randomSeed()
	}
	source := request.Source
	if source != SourceX00 && source != SourceCrew {
		source = SourceApp
	}
	now := s.now().UTC()
	return Item{
		ID:         newID(),
		Capability: strings.TrimSpace(request.Capability),
		Kind:       file.Kind,
		Prompt:     cleanText(prompt, MaxPromptRunes),
		Negative:   cleanText(negative, MaxNegativeRunes),
		Width:      width,
		Height:     height,
		Seconds:    seconds,
		Seed:       seed,
		Input:      input,
		Source:     source,
		State:      StateQueued,
		Files:      []File{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// sizes applies the capability's defaults and the gateway's bounds. Sides are
// rounded down to a multiple of 16, as the models need.
func sizes(file workflowFile, request Request) (int, int, int, error) {
	switch file.Kind {
	case KindAudio:
		seconds := pick(request.Seconds, file.Defaults.Seconds, 20)
		if seconds < 1 || seconds > MaxAudioSeconds {
			return 0, 0, 0, ErrInvalidRequest
		}
		return 0, 0, seconds, nil
	case KindVideo:
		width, height := pick(request.Width, file.Defaults.Width, 832), pick(request.Height, file.Defaults.Height, 480)
		seconds := pick(request.Seconds, file.Defaults.Seconds, 2)
		if !validSide(width, MaxVideoSide) || !validSide(height, MaxVideoSide) || seconds < 1 || seconds > MaxVideoSeconds {
			return 0, 0, 0, ErrInvalidRequest
		}
		return width / 16 * 16, height / 16 * 16, seconds, nil
	default:
		width, height := pick(request.Width, file.Defaults.Width, 1024), pick(request.Height, file.Defaults.Height, 1024)
		if !validSide(width, MaxImageSide) || !validSide(height, MaxImageSide) {
			return 0, 0, 0, ErrInvalidRequest
		}
		return width / 16 * 16, height / 16 * 16, 0, nil
	}
}

func pick(asked, preset, fallback int) int {
	if asked > 0 {
		return asked
	}
	if preset > 0 {
		return preset
	}
	return fallback
}

func validSide(side, most int) bool { return side >= MinSide && side <= most }

// Enabled is true when a GPU server is set.
func (s *Service) Enabled() bool { return s.server != nil }

// MakePicture makes one picture for a run and waits for it: the crew's own
// request, kept in the library like any other.
func (s *Service) MakePicture(ctx context.Context, prompt string, width, height int) ([]byte, error) {
	item, err := s.Create(Request{Capability: KindImage, Prompt: prompt, Width: width, Height: height, Source: SourceCrew})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, imageTimeout+wakeTimeout)
	defer cancel()
	for {
		current, err := s.lib.get(item.ID)
		if err != nil {
			return nil, err
		}
		switch current.State {
		case StateDone:
			if len(current.Files) == 0 || !ValidFileName(current.Files[0].Name) {
				return nil, failure(FailNoOutput)
			}
			return os.ReadFile(filepath.Join(s.lib.itemDir(item.ID), current.Files[0].Name)) //nolint:gosec // a file in the library's own item folder
		case StateFailed:
			return nil, failure(current.Failure)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.pollEvery):
		}
	}
}

// Upload adds a picture from the person's computer to the library, so a
// capability can work on it. It needs no server.
func (s *Service) Upload(name string, data []byte) (Item, error) {
	if len(data) == 0 || len(data) > MaxUploadBytes {
		return Item{}, ErrInvalidRequest
	}
	ext, contentType := sniffPicture(data)
	if ext == "" {
		return Item{}, ErrInvalidRequest
	}
	now := s.now().UTC()
	item := Item{
		ID:         newID(),
		Capability: "upload",
		Kind:       KindImage,
		Prompt:     cleanLine(name, 120),
		Source:     SourceUpload,
		State:      StateDone,
		Files:      []File{{Name: "1" + ext, Type: contentType, Bytes: int64(len(data))}},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if item.Prompt == "" {
		item.Prompt = "upload"
	}
	dir := s.lib.itemDir(item.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Item{}, err
	}
	if err := writeFile(dir, item.Files[0].Name, data); err != nil {
		_ = os.RemoveAll(dir)
		return Item{}, err
	}
	if err := s.lib.save(item); err != nil {
		_ = os.RemoveAll(dir)
		return Item{}, err
	}
	return item, nil
}

// Delete removes an item and its files, stopping its work first.
func (s *Service) Delete(id string) error {
	if !ValidItemID(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return s.lib.remove(id)
}

func (s *Service) done() {
	s.mu.Lock()
	s.pending--
	s.mu.Unlock()
}

func (s *Service) interruptLeftovers() {
	items, err := s.lib.list()
	if err != nil {
		return
	}
	for _, item := range items {
		if item.State == StateQueued || item.State == StateRunning {
			s.fail(item.ID, FailInterrupted)
		}
	}
}

func (s *Service) fail(id, code string) {
	_ = s.lib.update(id, func(item *Item) {
		item.State = StateFailed
		item.Failure = code
		item.UpdatedAt = s.now().UTC()
	})
}

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case id := <-s.queue:
			s.run(id)
		}
	}
}

func (s *Service) run(id string) {
	defer s.done()
	item, err := s.lib.get(id)
	if err != nil || item.State != StateQueued {
		return
	}
	file, ok := s.workflows()[item.Capability]
	if !ok {
		s.fail(id, FailRejected)
		return
	}
	timeout := imageTimeout
	if item.Kind == KindVideo {
		timeout = videoTimeout
	}
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.cancels, id)
		s.mu.Unlock()
		cancel()
	}()
	if err := s.lib.update(id, func(item *Item) { item.State = StateRunning; item.UpdatedAt = s.now().UTC() }); err != nil {
		return
	}
	files, err := s.wakeAndMake(ctx, item, file)
	if err != nil {
		// Deleted, or the app is closing: nothing to report.
		if errors.Is(err, context.Canceled) || s.ctx.Err() != nil {
			return
		}
		s.fail(id, codeOf(err))
		return
	}
	_ = s.lib.update(id, func(item *Item) {
		item.State = StateDone
		item.Files = files
		item.UpdatedAt = s.now().UTC()
	})
}

// wakeAndMake starts the server first when the app may switch it on.
func (s *Service) wakeAndMake(ctx context.Context, item Item, file workflowFile) ([]File, error) {
	if err := s.wake(ctx); err != nil {
		return nil, err
	}
	return s.make(ctx, item, file)
}

// make runs one item on the server and keeps what it saved.
func (s *Service) make(ctx context.Context, item Item, file workflowFile) ([]File, error) {
	values := renderValues{
		Prompt: item.Prompt, Negative: item.Negative, Prefix: "xrobot/" + item.ID,
		Seed: item.Seed, Width: item.Width, Height: item.Height, Seconds: item.Seconds,
	}
	if item.Input != "" {
		ext, data, err := s.inputPicture(item.Input)
		if err != nil {
			return nil, failure(FailRejected)
		}
		name, err := s.server.upload(ctx, "xrobot-"+item.ID+ext, data)
		if err != nil {
			return nil, err
		}
		values.Image = name
	}
	promptID, err := s.server.submit(ctx, render(file.Workflow, values), item.ID)
	if err != nil {
		return nil, err
	}
	refs, err := s.await(ctx, promptID)
	if err != nil {
		return nil, err
	}
	return s.keep(ctx, item, refs)
}

// await polls the server until the prompt has run. A few missed polls are
// forgiven: a rented server's network can blink.
func (s *Service) await(ctx context.Context, promptID string) ([]outputRef, error) {
	misses := 0
	for {
		refs, finished, err := s.server.history(ctx, promptID)
		switch {
		case err == nil && finished:
			return refs, nil
		case err == nil:
			misses = 0
		case ctx.Err() != nil:
			return nil, ctx.Err()
		default:
			var failed *serverError
			if errors.As(err, &failed) && failed.code != FailUnreachable {
				return nil, err
			}
			misses++
			if misses >= maxPollMisses {
				return nil, err
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.pollEvery):
		}
	}
}

// keep downloads what the server saved into the item's folder, checking each
// file is the media its name says.
func (s *Service) keep(ctx context.Context, item Item, refs []outputRef) ([]File, error) {
	dir := s.lib.itemDir(item.ID)
	files := []File{}
	for _, ref := range refs {
		if len(files) == maxFilesPerItem {
			break
		}
		ext := strings.ToLower(path.Ext(ref.Filename))
		if ext == ".jpeg" {
			ext = ".jpg"
		}
		contentType := mediaTypes[ext]
		if !fitsKind(item.Kind, contentType) {
			continue
		}
		tmp, err := os.CreateTemp(dir, ".download-*")
		if err != nil {
			return nil, err
		}
		written, err := s.server.download(ctx, ref, tmp, limitFor(item.Kind))
		closeErr := tmp.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(tmp.Name())
			return nil, err
		}
		if !matchesType(tmp.Name(), contentType) {
			_ = os.Remove(tmp.Name())
			continue
		}
		name := fmt.Sprintf("%d%s", len(files)+1, ext)
		if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
			_ = os.Remove(tmp.Name())
			return nil, err
		}
		files = append(files, File{Name: name, Type: contentType, Bytes: written})
	}
	if len(files) == 0 {
		return nil, failure(FailNoOutput)
	}
	return files, nil
}

// inputPicture reads the picture of a finished library item.
func (s *Service) inputPicture(id string) (string, []byte, error) {
	item, err := s.lib.get(id)
	if err != nil {
		return "", nil, err
	}
	if item.State != StateDone || item.Kind != KindImage || len(item.Files) == 0 {
		return "", nil, ErrInvalidRequest
	}
	file := item.Files[0]
	if !ValidFileName(file.Name) || !strings.HasPrefix(file.Type, "image/") {
		return "", nil, ErrInvalidRequest
	}
	full := filepath.Join(s.lib.itemDir(id), file.Name)
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxImageBytes {
		return "", nil, ErrInvalidRequest
	}
	data, err := os.ReadFile(full) //nolint:gosec // a file inside the library's own item folder
	if err != nil {
		return "", nil, err
	}
	ext, _ := sniffPicture(data)
	if ext == "" {
		return "", nil, ErrInvalidRequest
	}
	return ext, data, nil
}

// ValidFileName is true for a name the library gives a file: "<n>.<ext>".
func ValidFileName(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	if _, ok := mediaTypes[ext]; !ok {
		return false
	}
	digits := strings.TrimSuffix(name, path.Ext(name))
	if digits == "" || len(digits) > 2 {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ContentType is the type the library serves a file name as ("" if none).
func ContentType(name string) string { return mediaTypes[strings.ToLower(path.Ext(name))] }

func fitsKind(kind, contentType string) bool {
	switch kind {
	case KindVideo:
		// Animated WebP and GIF are videos too.
		return strings.HasPrefix(contentType, "video/") || contentType == "image/webp" || contentType == "image/gif"
	case KindAudio:
		return strings.HasPrefix(contentType, "audio/")
	default:
		return strings.HasPrefix(contentType, "image/")
	}
}

func limitFor(kind string) int64 {
	switch kind {
	case KindVideo:
		return maxVideoBytes
	case KindAudio:
		return maxAudioBytes
	default:
		return maxImageBytes
	}
}

// matchesType checks a file's first bytes against its type.
func matchesType(name, contentType string) bool {
	file, err := os.Open(name) //nolint:gosec // a temporary file this service just wrote
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	head := make([]byte, 16)
	n, _ := io.ReadFull(file, head)
	return headMatches(head[:n], contentType)
}

func headMatches(head []byte, contentType string) bool {
	has := func(offset int, magic string) bool {
		return len(head) >= offset+len(magic) && string(head[offset:offset+len(magic)]) == magic
	}
	switch contentType {
	case "image/png":
		return has(0, "\x89PNG\r\n\x1a\n")
	case "image/jpeg":
		return has(0, "\xff\xd8\xff")
	case "image/webp":
		return has(0, "RIFF") && has(8, "WEBP")
	case "image/gif":
		return has(0, "GIF87a") || has(0, "GIF89a")
	case "video/webm":
		return has(0, "\x1a\x45\xdf\xa3")
	case "video/mp4":
		return has(4, "ftyp")
	case "audio/flac":
		return has(0, "fLaC")
	case "audio/wav":
		return has(0, "RIFF") && has(8, "WAVE")
	case "audio/ogg":
		return has(0, "OggS")
	case "audio/mpeg":
		return has(0, "ID3") || (len(head) >= 2 && head[0] == 0xff && head[1]&0xe0 == 0xe0)
	}
	return false
}

// sniffPicture names a picture the library accepts as input.
func sniffPicture(data []byte) (string, string) {
	head := data
	if len(head) > 16 {
		head = head[:16]
	}
	for _, kind := range []struct{ ext, contentType string }{{".png", "image/png"}, {".jpg", "image/jpeg"}, {".webp", "image/webp"}} {
		if headMatches(head, kind.contentType) {
			return kind.ext, kind.contentType
		}
	}
	return "", ""
}

// writeFile writes a file whole: a temporary file first, then a rename.
func writeFile(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

func newID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("media: no randomness: %v", err))
	}
	return hex.EncodeToString(raw[:])
}

// randomSeed is a positive seed every sampler accepts.
func randomSeed() int64 {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return time.Now().UnixNano()&(1<<48-1) + 1
	}
	return int64(binary.BigEndian.Uint64(raw[:])&(1<<48-1)) + 1
}
