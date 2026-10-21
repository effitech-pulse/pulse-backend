package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config описывает операционный API; модель и данные остаются в Python-сервисе.
type Config struct {
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
		FileCheckMode:           env("FILE_CHECK_MODE", "off"),
		FileCheckManifest:       env("FILE_CHECK_MANIFEST", ""),
		FileCheckManifestSHA256: env("FILE_CHECK_MANIFEST_SHA256", ""),
		FileCheckRunID:          env("FILE_CHECK_RUN_ID", ""),
		FileJobProducerAMQPURL:  env("FILE_JOB_PRODUCER_AMQP_URL", ""),
		FileJobAMQPURL:          env("FILE_JOB_AMQP_URL", ""),
		AppScope:                env("APP_SCOPE", "cohort"),
		CohortVersion:           env("COHORT_VERSION", ""),
		Addr:                    env("HTTP_ADDR", "127.0.0.1:8000"),
		DatabaseURL:             strings.Replace(env("DATABASE_URL", "postgresql://esp:esp@localhost:5433/esp_app"), "postgresql+psycopg2://", "postgresql://", 1),
		APIKey:                  env("API_KEY", ""),
		AuthMode:                env("AUTH_MODE", "legacy"),
		Prefix:                  strings.TrimRight(env("API_V1_PREFIX", "/api/v1"), "/"),
		ParameterPath:           env("PARAM_DICT_PATH", "parameters_dict.json"),
		MLTarget:                env("ML_SERVICE_TARGET", "127.0.0.1:50051"),
		MLKey:                   env("ML_SERVICE_API_KEY", ""),
		MetabaseURL:             strings.TrimRight(env("METABASE_SITE_URL", ""), "/"),
		MetabaseSecret:          env("METABASE_EMBED_SECRET", ""),
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
