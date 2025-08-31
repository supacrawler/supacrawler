package parser

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// ParseQuery binds the query string to a struct, using the 'form' tag.
// This is a custom parser to work around fiber's default QueryParser
// which incorrectly expects a 'query' tag.
func ParseQuery(c *fiber.Ctx, out interface{}) error {
	// Ensure out is a pointer to a struct.
	val := reflect.ValueOf(out)
	if val.Kind() != reflect.Ptr || val.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("output must be a pointer to a struct")
	}

	elem := val.Elem()
	typ := elem.Type()

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fieldValue := elem.Field(i)

		// Get the tag and skip if not defined
		tag := field.Tag.Get("form")
		if tag == "" || tag == "-" {
			continue
		}

		// Parse the tag name
		tagName := strings.Split(tag, ",")[0]
		if tagName == "" {
			continue
		}

		// Get query parameter value from context
		queryValue := c.Query(tagName)
		if queryValue == "" {
			continue
		}

		// Set field value based on its type
		if err := setFieldValue(fieldValue, queryValue); err != nil {
			return fmt.Errorf("error setting field %s: %w", field.Name, err)
		}
	}

	return nil
}

func setFieldValue(field reflect.Value, value string) error {
	if !field.CanSet() {
		return nil // Skip unsettable fields
	}

	// Handle pointers
	if field.Kind() == reflect.Ptr {
		// If the pointer is nil, create a new instance of the element type.
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		// Dereference the pointer to set the underlying value.
		field = field.Elem()
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		field.SetInt(i)
	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(b)
	}

	return nil
}
