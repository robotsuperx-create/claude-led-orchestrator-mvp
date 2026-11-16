package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCommanderNeedsOnlyAClaudeKeyEvenWithRunsOff(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	writeOrchestratorSettings(t, dataDir, `{"version":1,"values":{
		"AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY":"file-key"
	}}`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	commander := cfg.ClaudeOrchestrator.Commander
	if cfg.ClaudeOrchestrator.FeatureEnabled || !commander.Enabled {
		t.Fatalf("commander = %v, runs enabled = %t", commander, cfg.ClaudeOrchestrator.FeatureEnabled)
	}
	if commander.Provider.BaseURL != DefaultCommanderBaseURL || commander.Provider.DefaultModel != DefaultCommanderModel || commander.Provider.APIKey != "file-key" {
		t.Fatalf("provider = %v", commander.Provider)
	}
	if strings.Contains(commander.String(), "file-key") || strings.Contains(commander.GoString(), "file-key") {
		t.Fatal("the key leaked into formatting")
	}
}

func TestCommanderSurvivesBrokenRunSettings(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	writeOrchestratorSettings(t, dataDir, `{"version":1,"values":{
		"AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED":"on",
		"AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY":"file-key",
		"AO_CLAUDE_ORCHESTRATOR_X00_MODEL":"claude-sonnet-5-5",
		"AO_CLAUDE_ORCHESTRATOR_WORKER_TIMEOUT":"banana"
	}}`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClaudeOrchestrator.ConfigError == "" {
		t.Fatal("expected incomplete run settings to be reported")
	}
	if commander := cfg.ClaudeOrchestrator.Commander; !commander.Enabled || commander.Provider.DefaultModel != "claude-sonnet-5-5" {
		t.Fatalf("commander = %v", commander)
	}
}

func TestCommanderReasons(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		reason string
	}{
		{"no key", map[string]string{}, CommanderReasonNoKey},
		{"turned off", map[string]string{"AO_CLAUDE_ORCHESTRATOR_X00_ENABLED": "off", "AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY": "k"}, CommanderReasonOff},
		{"plain http elsewhere", map[string]string{"AO_CLAUDE_ORCHESTRATOR_CLAUDE_BASE_URL": "http://example.com/v1", "AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY": "k"}, CommanderReasonInvalidURL},
		{"bad model", map[string]string{"AO_CLAUDE_ORCHESTRATOR_X00_MODEL": "a b", "AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY": "k"}, CommanderReasonBadModel},
	}
	for _, tc := range cases {
		got := loadCommanderConfig(func(name string) string { return tc.values[name] })
		if got.Enabled || got.Reason != tc.reason {
			t.Fatalf("%s: got %v", tc.name, got)
		}
	}
	local := loadCommanderConfig(func(name string) string {
		if name == "AO_CLAUDE_ORCHESTRATOR_CLAUDE_BASE_URL" {
			return "http://127.0.0.1:11434/v1"
		}
		return ""
	})
	if !local.Enabled {
		t.Fatalf("a keyless loopback endpoint should be allowed: %v", local)
	}
}

func TestX00VoiceUsesLocalEnginesUntilACloudKeyIsSaved(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	voice := cfg.ClaudeOrchestrator.Voice
	if voice.Cloud != nil || voice.CloudReason != "" || voice.SpeechDir != filepath.Join(dataDir, X00SpeechDirName) {
		t.Fatalf("voice = %+v", voice)
	}
	writeOrchestratorSettings(t, dataDir, `{"version":1,"values":{
		"AO_CLAUDE_ORCHESTRATOR_X00_VOICE_API_KEY":"voice-secret",
		"AO_CLAUDE_ORCHESTRATOR_X00_VOICE_NAME":"ash"
	}}`)
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cloud := cfg.ClaudeOrchestrator.Voice.Cloud
	if cloud == nil || cloud.BaseURL != DefaultX00VoiceBaseURL || cloud.TranscribeModel != DefaultX00VoiceTranscribeModel || cloud.SpeechModel != DefaultX00VoiceSpeechModel || cloud.Voice != "ash" || cloud.APIKey != "voice-secret" {
		t.Fatalf("cloud = %v", cloud)
	}
	if strings.Contains(cloud.String(), "voice-secret") || strings.Contains(cloud.GoString(), "voice-secret") {
		t.Fatal("the voice key leaked into formatting")
	}
}

func TestX00VoiceRejectsBadCloudSettingsWithoutStoppingTheDaemon(t *testing.T) {
	for name, tc := range map[string]struct{ values, reason string }{
		"plain http off-machine": {`"AO_CLAUDE_ORCHESTRATOR_X00_VOICE_BASE_URL":"http://example.com/v1"`, CommanderReasonInvalidURL},
		"credentials in URL":     {`"AO_CLAUDE_ORCHESTRATOR_X00_VOICE_BASE_URL":"https://user:pw@example.com/v1"`, CommanderReasonInvalidURL},
		"model with a path":      {`"AO_CLAUDE_ORCHESTRATOR_X00_VOICE_TTS_MODEL":"../../admin"`, CommanderReasonBadModel},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			t.Setenv("AO_DATA_DIR", dataDir)
			writeOrchestratorSettings(t, dataDir, `{"version":1,"values":{"AO_CLAUDE_ORCHESTRATOR_X00_VOICE_API_KEY":"k",`+tc.values+`}}`)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if voice := cfg.ClaudeOrchestrator.Voice; voice.Cloud != nil || voice.CloudReason != tc.reason {
				t.Fatalf("voice = %+v", voice)
			}
		})
	}
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	speech := filepath.Join(t.TempDir(), "engines")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_X00_SPEECH_DIR", speech)
	cfg, err := Load()
	if err != nil || cfg.ClaudeOrchestrator.Voice.SpeechDir != speech {
		t.Fatalf("speech dir = %q, %v", cfg.ClaudeOrchestrator.Voice.SpeechDir, err)
	}
}

func TestMediaGatewayNeedsAValidServerAndVoiceCanGoThroughIt(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if media := cfg.ClaudeOrchestrator.Media; media.GPU != nil || media.Reason != MediaReasonNoServer || media.LibraryDir != filepath.Join(dataDir, MediaDirName) {
		t.Fatalf("media with nothing set = %+v", media)
	}
	for name, tc := range map[string]struct{ values, reason string }{
		"plain http off-machine": {`"AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_URL":"http://203.0.113.9:8188","AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_TOKEN":"t"`, MediaReasonInvalidURL},
		"credentials in URL":     {`"AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_URL":"https://u:p@gpu.example.com","AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_TOKEN":"t"`, MediaReasonInvalidURL},
		"no token off-machine":   {`"AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_URL":"https://gpu.example.com"`, MediaReasonNoToken},
		"token with a space":     {`"AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_URL":"https://gpu.example.com","AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_TOKEN":"a b"`, MediaReasonBadToken},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			t.Setenv("AO_DATA_DIR", dataDir)
			writeOrchestratorSettings(t, dataDir, `{"version":1,"values":{`+tc.values+`}}`)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if media := cfg.ClaudeOrchestrator.Media; media.GPU != nil || media.Reason != tc.reason {
				t.Fatalf("media = %+v", media)
			}
		})
	}

	dataDir = t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	writeOrchestratorSettings(t, dataDir, `{"version":1,"values":{
		"AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_URL":"https://gpu.example.com/",
		"AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_TOKEN":"gpu-secret",
		"AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_VOICE":"on"
	}}`)
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	gpu := cfg.ClaudeOrchestrator.Media.GPU
	if gpu == nil || gpu.BaseURL != "https://gpu.example.com" || gpu.Token != "gpu-secret" || !gpu.Voice {
		t.Fatalf("gpu = %v", gpu)
	}
	if strings.Contains(gpu.String(), "gpu-secret") || strings.Contains(gpu.GoString(), "gpu-secret") {
		t.Fatal("the GPU token leaked into formatting")
	}
	cloud := cfg.ClaudeOrchestrator.Voice.Cloud
	if cloud == nil || !cloud.GPU || cloud.BaseURL != "https://gpu.example.com/v1" || cloud.APIKey != "gpu-secret" || cloud.TranscribeModel != DefaultMediaVoiceTranscribeModel || cloud.SpeechModel != DefaultMediaVoiceSpeechModel || cloud.Voice != DefaultMediaVoiceName {
		t.Fatalf("voice through the GPU server = %v", cloud)
	}

	// A model that climbs a path is refused here too; a voice key of its own wins.
	writeOrchestratorSettings(t, dataDir, `{"version":1,"values":{
		"AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_URL":"http://127.0.0.1:8188",
		"AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_VOICE":"on",
		"AO_CLAUDE_ORCHESTRATOR_X00_VOICE_TTS_MODEL":"../admin/x"
	}}`)
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClaudeOrchestrator.Media.GPU == nil || cfg.ClaudeOrchestrator.Media.GPU.Token != "" {
		t.Fatalf("a server on this machine needs no token: %+v", cfg.ClaudeOrchestrator.Media)
	}
	if voice := cfg.ClaudeOrchestrator.Voice; voice.Cloud != nil || voice.CloudReason != CommanderReasonBadModel {
		t.Fatalf("voice = %+v", voice)
	}
	writeOrchestratorSettings(t, dataDir, `{"version":1,"values":{
		"AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_URL":"http://127.0.0.1:8188",
		"AO_CLAUDE_ORCHESTRATOR_MEDIA_GPU_VOICE":"on",
		"AO_CLAUDE_ORCHESTRATOR_X00_VOICE_API_KEY":"own-key"
	}}`)
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cloud := cfg.ClaudeOrchestrator.Voice.Cloud; cloud == nil || cloud.GPU || cloud.APIKey != "own-key" {
		t.Fatalf("a voice key of its own = %v", cloud)
	}
}
