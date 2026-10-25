package config

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config описывает операционный API; модель и данные остаются в Python-сервисе.
type Config struct {
	DailyFlowMode                string
	CollectionJobMode            string
	CollectionJobManifest        string
	CollectionJobManifestSHA256  string
	CollectionJobRunID           string
	CollectionJobTestSchema      string
	CollectionJobAMQPURL         string
	CollectionJobProducerAMQPURL string

	CohortJobMode            string
	CohortJobManifest        string
	CohortJobManifestSHA256  string
	CohortJobRunID           string
	CohortJobTestSchema      string
	CohortJobMLTarget        string
	CohortJobAMQPURL         string
	CohortJobProducerAMQPURL string

	FileCheckMode           string
	FileCheckManifest       string
	FileCheckManifestSHA256 string
	FileCheckRunID          string
	FileJobAMQPURL          string
	FileJobProducerAMQPURL  string

	AppScope       string
	CohortVersion  string
	Addr           string
	DatabaseURL    string
	APIKey         string
	AuthMode       string
	RBACMode       string
	CookieSecure   bool
	Prefix         string
	ParameterPath  string
	MLTarget       string
	MLKey          string
	PoolSize       int32
	CORS           []string
	MLTimeout      time.Duration
	AutoRun        bool
	AutoRunHour    int
	Location       *time.Location
	MetabaseURL    string
	MetabaseSecret string
	Dashboards     []Dashboard
	TokenTTL       int64
}

type Dashboard struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	return strconv.Atoi(env(key, strconv.Itoa(fallback)))
}

func Load() (Config, error) {
	_ = godotenv.Load(".env")
	c := Config{
		DailyFlowMode:                env("DAILY_FLOW_MODE", "off"),
		CollectionJobMode:            env("COLLECTION_JOB_MODE", "off"),
		CollectionJobManifest:        env("COLLECTION_JOB_MANIFEST", ""),
		CollectionJobManifestSHA256:  env("COLLECTION_JOB_MANIFEST_SHA256", ""),
		CollectionJobRunID:           env("COLLECTION_JOB_RUN_ID", ""),
		CollectionJobTestSchema:      env("COLLECTION_JOB_TEST_SCHEMA", ""),
		CollectionJobAMQPURL:         env("COLLECTION_JOB_AMQP_URL", ""),
		CollectionJobProducerAMQPURL: env("COLLECTION_JOB_PRODUCER_AMQP_URL", ""),

		CohortJobMode:            env("COHORT_JOB_MODE", "off"),
		CohortJobManifest:        env("COHORT_JOB_MANIFEST", ""),
		CohortJobManifestSHA256:  env("COHORT_JOB_MANIFEST_SHA256", ""),
		CohortJobRunID:           env("COHORT_JOB_RUN_ID", ""),
		CohortJobTestSchema:      env("COHORT_JOB_TEST_SCHEMA", ""),
		CohortJobMLTarget:        env("COHORT_JOB_ML_TARGET", ""),
		CohortJobAMQPURL:         env("COHORT_JOB_AMQP_URL", ""),
		CohortJobProducerAMQPURL: env("COHORT_JOB_PRODUCER_AMQP_URL", ""),
		FileCheckMode:            env("FILE_CHECK_MODE", "off"),
		FileCheckManifest:        env("FILE_CHECK_MANIFEST", ""),
		FileCheckManifestSHA256:  env("FILE_CHECK_MANIFEST_SHA256", ""),
		FileCheckRunID:           env("FILE_CHECK_RUN_ID", ""),
		FileJobProducerAMQPURL:   env("FILE_JOB_PRODUCER_AMQP_URL", ""),
		FileJobAMQPURL:           env("FILE_JOB_AMQP_URL", ""),
		AppScope:                 env("APP_SCOPE", "cohort"),
		CohortVersion:            env("COHORT_VERSION", ""),
		Addr:                     env("HTTP_ADDR", "127.0.0.1:8000"),
		DatabaseURL:              strings.Replace(env("DATABASE_URL", "postgresql://esp:esp@localhost:5433/esp_app"), "postgresql+psycopg2://", "postgresql://", 1),
		APIKey:                   env("API_KEY", ""),
		AuthMode:                 env("AUTH_MODE", "legacy"),
		RBACMode:                 env("RBAC_MODE", "compat"),
		Prefix:                   strings.TrimRight(env("API_V1_PREFIX", "/api/v1"), "/"),
		ParameterPath:            env("PARAM_DICT_PATH", "parameters_dict.json"),
		MLTarget:                 env("ML_SERVICE_TARGET", "127.0.0.1:50051"),
		MLKey:                    env("ML_SERVICE_API_KEY", ""),
		MetabaseURL:              strings.TrimRight(env("METABASE_SITE_URL", ""), "/"),
		MetabaseSecret:           env("METABASE_EMBED_SECRET", ""),
	}

	if err := ValidateDailyFlow(c); err != nil {
		return c, err
	}
	if err := validateCollectionJobConfig(c); err != nil {
		return c, err
	}
	if err := validateCohortJobConfig(c); err != nil {
		return c, err
	}

	allowReal, realErr := strconv.ParseBool(env("FILE_CHECK_ALLOW_REAL", "false"))
	if realErr != nil || allowReal {
		return c, fmt.Errorf("FILE_CHECK_ALLOW_REAL must be false")
	}
	if c.FileCheckMode != "off" && c.FileCheckMode != "synthetic" {
		return c, fmt.Errorf("FILE_CHECK_MODE must be off or synthetic")
	}
	if c.FileCheckMode == "synthetic" && (c.AuthMode != "local" || c.AppScope != "cohort" || c.FileCheckManifest == "" || c.FileCheckManifestSHA256 == "" || c.FileCheckRunID == "") {
		return c, fmt.Errorf("synthetic file checks require local cohort and explicit manifest/hash/run")
	}

	if c.AppScope != "cohort" && c.AppScope != "research" {
		return c, fmt.Errorf("APP_SCOPE must be cohort or research")
	}
	if c.AppScope == "cohort" && strings.TrimSpace(c.CohortVersion) == "" {
		return c, fmt.Errorf("COHORT_VERSION is required for APP_SCOPE=cohort")
	}
	if c.AuthMode != "legacy" && c.AuthMode != "local" {
		return c, fmt.Errorf("AUTH_MODE must be legacy or local")
	}
	if c.RBACMode != "compat" && c.RBACMode != "enforce" {
		return c, fmt.Errorf("RBAC_MODE must be compat or enforce")
	}
	var cookieErr error
	c.CookieSecure, cookieErr = strconv.ParseBool(env("AUTH_COOKIE_SECURE", "true"))
	if cookieErr != nil {
		return c, cookieErr
	}
	for _, v := range strings.Split(env("CORS_ORIGINS", "http://localhost:3000,http://localhost:5173"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			c.CORS = append(c.CORS, v)
		}
	}
	timeout, err := strconv.ParseFloat(env("ML_SERVICE_TIMEOUT_S", "300"), 64)
	if err != nil || timeout <= 0 || math.IsNaN(timeout) || math.IsInf(timeout, 0) {
		return c, fmt.Errorf("ML_SERVICE_TIMEOUT_S must be positive")
	}
	c.MLTimeout = time.Duration(timeout * float64(time.Second))
	if c.MLTimeout <= 0 {
		return c, fmt.Errorf("ML_SERVICE_TIMEOUT_S must be at least 1 ns")
	}
	size, err := envInt("DB_POOL_SIZE", 5)
	if err != nil || size < 2 || size > 1000 {
		return c, fmt.Errorf("DB_POOL_SIZE must be between 2 and 1000")
	}
	c.PoolSize = int32(size)
	if c.CollectionJobMode == "synthetic" && size > 8 {
		return c, fmt.Errorf("collection pool exceeds 8")
	}
	c.AutoRun, err = strconv.ParseBool(env("ML_AUTORUN_ENABLED", "true"))
	if err != nil {
		return c, err
	}
	c.AutoRunHour, err = envInt("ML_AUTORUN_HOUR", 5)
	if err != nil || c.AutoRunHour < 0 || c.AutoRunHour > 23 {
		return c, fmt.Errorf("invalid ML_AUTORUN_HOUR")
	}
	c.Location, err = time.LoadLocation(env("TZ", "Europe/Moscow"))
	if err != nil {
		return c, err
	}
	ttl, err := envInt("METABASE_TOKEN_TTL_S", 3600)
	if err != nil || ttl < 1 {
		return c, fmt.Errorf("invalid METABASE_TOKEN_TTL_S")
	}
	c.TokenTTL = int64(ttl)
	c.Dashboards = []Dashboard{}
	if err := json.Unmarshal([]byte(env("METABASE_DASHBOARDS", "[]")), &c.Dashboards); err != nil {
		return c, fmt.Errorf("METABASE_DASHBOARDS: %w", err)
	}
	if c.Dashboards == nil {
		c.Dashboards = []Dashboard{}
	}
	return c, nil
}

func validateCohortJobConfig(c Config) error {
	allow, err := strconv.ParseBool(env("COHORT_JOB_ALLOW_REAL", "false"))
	if err != nil || allow {
		return fmt.Errorf("COHORT_JOB_ALLOW_REAL must be false")
	}
	if c.CohortJobMode != "off" && c.CohortJobMode != "synthetic" {
		return fmt.Errorf("COHORT_JOB_MODE must be off or synthetic")
	}
	if c.CohortJobMode == "off" {
		return nil
	}
	sha := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	schema := regexp.MustCompile(`^[a-z][a-z0-9_]{2,62}$`)
	if c.AuthMode != "local" || c.AppScope != "cohort" || c.CohortJobManifest == "" || !sha.MatchString(c.CohortJobManifestSHA256) || strings.TrimSpace(c.CohortJobRunID) == "" || !schema.MatchString(c.CohortJobTestSchema) || c.CohortJobTestSchema == "public" || strings.HasPrefix(c.CohortJobTestSchema, "pg_") || c.CohortJobTestSchema == "information_schema" || strings.TrimSpace(c.CohortJobMLTarget) == "" || strings.TrimSpace(c.CohortJobMLTarget) == strings.TrimSpace(c.MLTarget) {
		return fmt.Errorf("synthetic cohort jobs require local cohort, explicit trusted manifest/hash/run/schema and separate ML target")
	}
	for _, raw := range []string{c.CohortJobAMQPURL, c.CohortJobProducerAMQPURL} {
		u, e := url.Parse(raw)
		if e != nil || (u.Scheme != "amqp" && u.Scheme != "amqps") || u.Host == "" || u.Path != "/pulse-q2-synthetic" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("synthetic cohort jobs require isolated AMQP URLs")
		}
	}
	return nil
}

func validateCollectionJobConfig(c Config) error {
	if c.CollectionJobMode != "off" && c.CollectionJobMode != "synthetic" {
		return fmt.Errorf("COLLECTION_JOB_MODE must be off or synthetic")
	}
	if c.CollectionJobMode == "off" {
		return nil
	}
	if c.AuthMode != "local" || c.AppScope != "research" || c.CollectionJobManifest == "" || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(c.CollectionJobManifestSHA256) || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(c.CollectionJobRunID) || !regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`).MatchString(c.CollectionJobTestSchema) || c.CollectionJobTestSchema == "public" || c.CollectionJobTestSchema == "information_schema" || strings.HasPrefix(c.CollectionJobTestSchema, "pg_") {
		return fmt.Errorf("synthetic collection requires local research and trusted manifest/hash/run/test schema")
	}
	for _, raw := range []string{c.CollectionJobAMQPURL, c.CollectionJobProducerAMQPURL} {
		u, e := url.Parse(raw)
		if e != nil || (u.Scheme != "amqp" && u.Scheme != "amqps") || u.Host == "" || u.Path != "/pulse-q2c2-synthetic" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("synthetic collection requires isolated AMQP URLs")
		}
	}
	return nil
}

// ValidateDailyFlow разрешает только фиксированный тестовый профиль.
func ValidateDailyFlow(c Config) error {
	if c.DailyFlowMode == "" || c.DailyFlowMode == "off" {
		return nil
	}
	if c.DailyFlowMode != "synthetic" {
		return fmt.Errorf("DAILY_FLOW_MODE must be off or synthetic")
	}
	if c.AuthMode != "local" || c.AppScope != "cohort" || c.CohortVersion != "presentation-v1" {
		return fmt.Errorf("DAILY_FLOW_MODE synthetic requires local cohort presentation-v1")
	}
	return nil
}
