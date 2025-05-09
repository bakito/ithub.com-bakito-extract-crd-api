package flags

import "fmt"

type ArrayFlags []string

// String is an implementation of the flag.Value interface.
func (i *ArrayFlags) String() string {
	return fmt.Sprintf("%v", *i)
}

// Set is an implementation of the flag.Value interface.
func (i *ArrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}
