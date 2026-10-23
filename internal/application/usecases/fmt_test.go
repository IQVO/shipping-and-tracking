package usecases_test

import "fmt"

// fmtAll prints a value with every verb that could leak a field.
func fmtAll(v any) string { return fmt.Sprintf("%v %+v %#v", v, v, v) }
