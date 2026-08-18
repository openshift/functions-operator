package config

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestParseNotificationSettings_DefaultsWhenAbsent(t *testing.T) {
	defaults := NotificationSettings{
		Mode:                      "http",
		KafkaBrokers:              []string{"b1:9092"},
		KafkaNotificationsTopics:  []string{"t1"},
		KafkaNotificationsGroupID: "g1",
	}

	got, err := parseNotificationSettings(map[string]string{}, defaults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, defaults) {
		t.Fatalf("expected defaults %+v, got %+v", defaults, got)
	}
}

func TestParseNotificationSettings_ConfigMapOverrides(t *testing.T) {
	defaults := NotificationSettings{Mode: "http"}
	data := map[string]string{
		"NOTIFICATIONS_MODE":           "kafka",
		"KAFKA_BROKERS":                "b1:9092, b2:9092 ",
		"KAFKA_NOTIFICATIONS_TOPICS":   "t1,t2",
		"KAFKA_NOTIFICATIONS_GROUP_ID": "grp",
	}

	got, err := parseNotificationSettings(data, defaults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := NotificationSettings{
		Mode:                      "kafka",
		KafkaBrokers:              []string{"b1:9092", "b2:9092"},
		KafkaNotificationsTopics:  []string{"t1", "t2"},
		KafkaNotificationsGroupID: "grp",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestParseNotificationSettings_InvalidMode(t *testing.T) {
	_, err := parseNotificationSettings(map[string]string{"NOTIFICATIONS_MODE": "bogus"}, NotificationSettings{})
	if err == nil {
		t.Fatal("expected error for invalid mode, got nil")
	}
}

func TestParseNotificationSettings_KafkaRequirements(t *testing.T) {
	tests := []struct {
		name string
		data map[string]string
	}{
		{
			name: "missing brokers",
			data: map[string]string{
				"NOTIFICATIONS_MODE":           "kafka",
				"KAFKA_NOTIFICATIONS_TOPICS":   "t1",
				"KAFKA_NOTIFICATIONS_GROUP_ID": "g1",
			},
		},
		{
			name: "missing topics",
			data: map[string]string{
				"NOTIFICATIONS_MODE":           "kafka",
				"KAFKA_BROKERS":                "b1:9092",
				"KAFKA_NOTIFICATIONS_GROUP_ID": "g1",
			},
		},
		{
			name: "missing group id",
			data: map[string]string{
				"NOTIFICATIONS_MODE":         "kafka",
				"KAFKA_BROKERS":              "b1:9092",
				"KAFKA_NOTIFICATIONS_TOPICS": "t1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseNotificationSettings(tt.data, NotificationSettings{}); err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestParseNotificationSettings_EmptyModeDefaultsToHTTP(t *testing.T) {
	got, err := parseNotificationSettings(map[string]string{}, NotificationSettings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Mode != "http" {
		t.Fatalf("expected mode http, got %q", got.Mode)
	}
}

func TestKafkaFingerprint_Stability(t *testing.T) {
	data := map[string][]byte{
		"protocol": []byte("SASL_SSL"),
		"user":     []byte("alice"),
		"password": []byte("s3cret"),
	}

	// Equal inputs from a separately-constructed map produce the same fingerprint
	// (independent of map iteration order).
	same := map[string][]byte{
		"password": []byte("s3cret"),
		"protocol": []byte("SASL_SSL"),
		"user":     []byte("alice"),
	}
	if kafkaFingerprint("kafka-creds", data) != kafkaFingerprint("kafka-creds", same) {
		t.Fatal("fingerprint is not stable for equal inputs")
	}

	// A changed value produces a different fingerprint (credential rotation).
	rotated := map[string][]byte{
		"protocol": []byte("SASL_SSL"),
		"user":     []byte("alice"),
		"password": []byte("rotated"),
	}
	if kafkaFingerprint("kafka-creds", data) == kafkaFingerprint("kafka-creds", rotated) {
		t.Fatal("fingerprint did not change after rotating a value")
	}

	// A changed secret name produces a different fingerprint.
	if kafkaFingerprint("kafka-creds", data) == kafkaFingerprint("other-creds", data) {
		t.Fatal("fingerprint did not change after changing the secret name")
	}

	// No secret (nil data, empty name) is stable and non-panicking, and differs
	// from any non-empty secret.
	if kafkaFingerprint("", nil) == kafkaFingerprint("kafka-creds", data) {
		t.Fatal("empty fingerprint collided with a populated secret")
	}
}

func TestDefaultConfigMapData(t *testing.T) {
	// With no kafka defaults, only NOTIFICATIONS_MODE is added (defaulting to http),
	// and no empty kafka keys are written.
	data := defaultConfigMapData(NotificationSettings{})
	if data["NOOBAA_ADAPTER_ID"] != defaultNoobaaAdapterID {
		t.Fatalf("expected default noobaa adapter id, got %q", data["NOOBAA_ADAPTER_ID"])
	}
	if data["NOTIFICATIONS_MODE"] != "http" {
		t.Fatalf("expected NOTIFICATIONS_MODE=http, got %q", data["NOTIFICATIONS_MODE"])
	}
	for _, k := range []string{"KAFKA_BROKERS", "KAFKA_NOTIFICATIONS_TOPICS", "KAFKA_NOTIFICATIONS_GROUP_ID"} {
		if _, ok := data[k]; ok {
			t.Fatalf("did not expect key %q when kafka defaults are empty", k)
		}
	}

	// With kafka defaults, they are serialized into the ConfigMap data.
	data = defaultConfigMapData(NotificationSettings{
		Mode:                      "kafka",
		KafkaBrokers:              []string{"b1:9092", "b2:9092"},
		KafkaNotificationsTopics:  []string{"t1", "t2"},
		KafkaNotificationsGroupID: "grp",
	})
	if data["NOTIFICATIONS_MODE"] != "kafka" {
		t.Fatalf("expected NOTIFICATIONS_MODE=kafka, got %q", data["NOTIFICATIONS_MODE"])
	}
	if data["KAFKA_BROKERS"] != "b1:9092,b2:9092" {
		t.Fatalf("unexpected KAFKA_BROKERS: %q", data["KAFKA_BROKERS"])
	}
	if data["KAFKA_NOTIFICATIONS_TOPICS"] != "t1,t2" {
		t.Fatalf("unexpected KAFKA_NOTIFICATIONS_TOPICS: %q", data["KAFKA_NOTIFICATIONS_TOPICS"])
	}
	if data["KAFKA_NOTIFICATIONS_GROUP_ID"] != "grp" {
		t.Fatalf("unexpected KAFKA_NOTIFICATIONS_GROUP_ID: %q", data["KAFKA_NOTIFICATIONS_GROUP_ID"])
	}

	// The produced data must parse back cleanly, yielding the same effective settings.
	cfg, err := parseConfig(&corev1.ConfigMap{Data: data}, NotificationSettings{})
	if err != nil {
		t.Fatalf("default ConfigMap data failed to parse: %v", err)
	}
	if cfg.Notifications.Mode != "kafka" {
		t.Fatalf("round-trip mode mismatch: %q", cfg.Notifications.Mode)
	}
}

func TestParseConfig_IncludesNotifications(t *testing.T) {
	cm := &corev1.ConfigMap{
		Data: map[string]string{
			"NOTIFICATIONS_MODE":           "kafka",
			"KAFKA_BROKERS":                "b1:9092",
			"KAFKA_NOTIFICATIONS_TOPICS":   "t1",
			"KAFKA_NOTIFICATIONS_GROUP_ID": "g1",
		},
	}

	cfg, err := parseConfig(cm, NotificationSettings{Mode: "http"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Notifications.Mode != "kafka" {
		t.Fatalf("expected kafka mode, got %q", cfg.Notifications.Mode)
	}
	if cfg.NoobaaAdapter.ID != "mcg-adapter" {
		t.Fatalf("expected default noobaa adapter id, got %q", cfg.NoobaaAdapter.ID)
	}
}
