package app

import (
	"encoding/json"
	"fmt"
	"github.com/joho/godotenv"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config описывает операционный API; модель и данные остаются в Python-сервисе.
type Config struct {
	Addr, DatabaseURL, APIKey, Prefix, ParameterPath, MLTarget, MLKey string
	CORS                                                              []string
	MLTimeout                                                         time.Duration
	AutoRun                                                           bool
	AutoRunHour                                                       int
	Location                                                          *time.Location
	MetabaseURL, MetabaseSecret                                       string
	Dashboards                                                        []Dashboard
	TokenTTL                                                          int64
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
func LoadConfig() (Config, error) {
	_ = godotenv.Load(".env")
	c := Config{Addr: env("HTTP_ADDR", "127.0.0.1:8000"), DatabaseURL: strings.Replace(env("DATABASE_URL", "postgresql://esp:esp@localhost:5433/esp_app"), "postgresql+psycopg2://", "postgresql://", 1), APIKey: env("API_KEY", ""), Prefix: strings.TrimRight(env("API_V1_PREFIX", "/api/v1"), "/"), ParameterPath: env("PARAM_DICT_PATH", "parameters_dict.json"), MLTarget: env("ML_SERVICE_TARGET", "127.0.0.1:50051"), MLKey: env("ML_SERVICE_API_KEY", ""), MetabaseURL: strings.TrimRight(env("METABASE_SITE_URL", ""), "/"), MetabaseSecret: env("METABASE_EMBED_SECRET", "")}
	for _, v := range strings.Split(env("CORS_ORIGINS", "http://localhost:3000,http://localhost:5173"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			c.CORS = append(c.CORS, v)
		}
	}
	timeout, err := strconv.ParseFloat(env("ML_SERVICE_TIMEOUT_S", "300"), 64)
	if err != nil || timeout <= 0 {
		return c, fmt.Errorf("ML_SERVICE_TIMEOUT_S must be positive")
	}
	c.MLTimeout = time.Duration(timeout * float64(time.Second))
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
	_ = json.Unmarshal([]byte(env("METABASE_DASHBOARDS", "[]")), &c.Dashboards)
	if c.Dashboards == nil {
		c.Dashboards = []Dashboard{}
	}
	return c, nil
}
