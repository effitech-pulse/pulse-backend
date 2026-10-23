// adminuser создаёт первого администратора; пароль поступает только через stdin.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func main() {
	observability.Install("unknown")
	if err := run(); err != nil {
		slog.Error("создание администратора не выполнено", "error_code", "operation_failed")
		os.Exit(1)
	}
}
func run() error {
	login := flag.String("login", "", "Логин первого администратора")
	name := flag.String("name", "", "Имя администратора")
	activate := flag.Bool("activate-rbac", false, "Активировать RBAC после остановки и drain")
	flag.Parse()
	if *activate {
		if *login != "" || *name != "" || flag.NArg() != 0 {
			return errors.New("-activate-rbac не совместим с bootstrap flags")
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pool, err := postgres.Open(ctx, cfg)
		if err != nil {
			return err
		}
		defer pool.Close()
		return postgres.New(pool).ActivateRBAC(ctx)
	}
	if *login == "" || *name == "" {
		return errors.New("укажите -login и -name; пароль передайте одной строкой через stdin")
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return errors.New("интерактивный ввод не поддерживается: передайте пароль через stdin из защищённого источника")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 256), 1024)
	if !scanner.Scan() {
		return errors.New("пароль не получен из stdin")
	}
	password := strings.TrimSuffix(scanner.Text(), "\r")
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	access, err := service.NewAccess(postgres.New(pool), true)
	if err != nil {
		return err
	}
	account, err := access.Bootstrap(ctx, domain.AccountInput{Login: *login, Name: *name, Password: &password})
	if err != nil {
		return err
	}
	slog.Info("первый администратор создан", "id", account.ID)
	_, err = fmt.Fprintln(os.Stdout, "Для персонального входа настройте AUTH_MODE=local и перезапустите API.")
	return err
}
