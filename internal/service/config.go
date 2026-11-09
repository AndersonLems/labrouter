package service

import (
	"context"
	"fmt"
	"lab-router/internal/model"
	"lab-router/internal/network"
	"lab-router/internal/store"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
)

type ConfigService struct {
	Store        *store.Store
	Live         bool
	Dir          string
	ApplyNetwork func(model.Config) error
}

func (s *ConfigService) Update(ctx context.Context, actor string, edit func(*model.Config) error) error {
	f, e := os.OpenFile(filepath.Join(s.Dir, "config.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); e != nil {
		return e
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	old, e := s.Store.Config()
	if e != nil {
		return e
	}
	c := old
	if e = edit(&c); e != nil {
		return e
	}
	if e = model.Validate(c); e != nil {
		return e
	}
	networkChanged := !sameNetwork(c, old)
	if networkChanged {
		if !s.Live {
			return fmt.Errorf("modo observação: alterações de rede exigem helper instalado e live mode")
		}
		apply := s.ApplyNetwork
		if apply == nil {
			apply = network.Apply
		}
		if _, e = s.Store.Backup(); e != nil {
			return e
		}
		if e = apply(c); e != nil {
			s.Store.Record("audit", actor, "network.apply", "falha; configuração não persistida: "+e.Error())
			return e
		}
	}
	if e = s.Store.Put("config", c); e != nil {
		if networkChanged {
			apply := s.ApplyNetwork
			if apply == nil {
				apply = network.Apply
			}
			if rollback := apply(old); rollback != nil {
				return fmt.Errorf("persistência e rollback falharam; consulte serviços")
			}
		}
		return e
	}
	s.Store.Record("audit", actor, "config.update", "sucesso")
	s.Store.Event("4 VALUE CHANGE", "")
	return nil
}
func sameNetwork(a, b model.Config) bool {
	a.CWMP = b.CWMP
	a.ParameterKey = b.ParameterKey
	return reflect.DeepEqual(a, b)
}
func (s *ConfigService) FactoryReset(ctx context.Context) error {
	return s.Update(ctx, "ACS", func(c *model.Config) error {
		defaults := model.Default()
		defaults.CWMP = c.CWMP
		defaults.WiFi.Channel = c.WiFi.Channel
		*c = defaults
		return nil
	})
}
