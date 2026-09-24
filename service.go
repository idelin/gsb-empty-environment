package canonicalkey

import (
	"context"
	"io"
	"time"
)

type Source interface {
	Open(ctx context.Context) (io.ReadCloser, error)
}

type SourceFunc func(ctx context.Context) (io.ReadCloser, error)

func (source SourceFunc) Open(ctx context.Context) (io.ReadCloser, error) {
	return source(ctx)
}

type Clock func() time.Time

type FailureStep string

const (
	FailureOpen      FailureStep = "open"
	FailureRead      FailureStep = "read"
	FailureCanonical FailureStep = "canonical"
	FailureCatalog   FailureStep = "catalog"
	FailureClose     FailureStep = "close"
)

type FailureFunc func(step FailureStep) error

type Service struct {
	Catalog Catalog
	Clock   Clock
	Limits  Limits
	Failure FailureFunc
}

type Result struct {
	Request   Request
	Canonical []byte
	Key       string
	Created   bool
	At        time.Time
}

func NewService(catalog Catalog, clock Clock, limits Limits) *Service {
	if catalog == nil {
		catalog = NewMemoryCatalog(DefaultHash)
	}
	if clock == nil {
		clock = time.Now
	}
	return &Service{Catalog: catalog, Clock: clock, Limits: limits}
}

func (service *Service) Key(request Request) (string, []byte, error) {
	canonical, err := CanonicalRequest(request, service.Limits)
	if err != nil {
		return "", nil, err
	}
	return DigestKey(canonical, DefaultHash), canonical, nil
}

func (service *Service) Resolve(ctx context.Context, source Source) (result Result, err error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if service.Failure != nil {
		if err := service.Failure(FailureOpen); err != nil {
			return Result{}, err
		}
	}
	readCloser, err := source.Open(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		readCloser.Close()
		return Result{}, err
	}
	defer func() {
		closeErr := readCloser.Close()
		if service.Failure != nil {
			if injected := service.Failure(FailureClose); injected != nil {
				err = injected
				return
			}
		}
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	if service.Failure != nil {
		if err := service.Failure(FailureRead); err != nil {
			return Result{}, err
		}
	}
	limits := service.Limits.withDefaults()
	data, err := io.ReadAll(io.LimitReader(readCloser, int64(limits.MaxInputBytes)+1))
	if err != nil {
		return Result{}, err
	}
	if len(data) > limits.MaxInputBytes {
		return Result{}, ErrBudgetExceeded
	}
	request, err := DecodeRequest(data, limits)
	if err != nil {
		return Result{}, err
	}
	if service.Failure != nil {
		if err := service.Failure(FailureCanonical); err != nil {
			return Result{}, err
		}
	}
	canonical, err := CanonicalRequest(request, limits)
	if err != nil {
		return Result{}, err
	}
	clock := service.Clock
	if clock == nil {
		clock = time.Now
	}
	catalog := service.Catalog
	if catalog == nil {
		catalog = NewMemoryCatalog(DefaultHash)
	}
	now := clock()
	if service.Failure != nil {
		if err := service.Failure(FailureCatalog); err != nil {
			return Result{}, err
		}
	}
	entry, created, err := catalog.GetOrInsert(ctx, canonical, now)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Request:   request,
		Canonical: canonical,
		Key:       entry.Key,
		Created:   created,
		At:        now,
	}, nil
}
