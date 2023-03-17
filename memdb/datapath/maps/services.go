package maps

import (
	"fmt"
	"math/rand"
)

type ServiceMap struct {
}

type ServiceKey []byte

type ServiceValue []byte

func (s *ServiceMap) Upsert(key ServiceKey, value ServiceValue) error {
	// Fail 30% of the time
	if rand.Intn(3) == 0 {
		return fmt.Errorf("oopsie")
	}
	return nil
}
