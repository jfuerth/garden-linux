package process

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Env map[string]string

func NewEnv(array []string) (Env, error) {
	env := make(Env, len(array))

	for _, str := range array {
		if str == "" {
			return nil, errors.New("malformed environment: empty string")
		}

		tokens := strings.Split(str, "=")

		if len(tokens) != 2 {
			return nil, fmt.Errorf("malformed environment: invalid format (not key=value): %q", str)
		}

		key, value := tokens[0], tokens[1]

		if key == "" {
			return nil, fmt.Errorf("malformed environment: empty key: %q", str)
		}

		env[key] = value
	}

	return env, nil
}

func (env Env) Merge(other Env) Env {
	merged := make(Env, len(env)+len(other))

	for key, value := range env {
		merged[key] = value
	}

	for key, value := range other {
		merged[key] = value
	}

	return merged
}

func (env Env) Array() []string {
	array := make([]string, len(env))

	i := 0
	for key, value := range env {
		array[i] = fmt.Sprintf("%s=%s", key, value)
		i++
	}

	sort.Strings(array)

	return array
}

func (env Env) String() string {
	return fmt.Sprintf("%#v", env)
}
