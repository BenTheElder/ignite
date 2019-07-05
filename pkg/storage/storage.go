package storage

import (
	"fmt"
	"path"

	"github.com/weaveworks/ignite/pkg/apis/ignite/scheme"
	meta "github.com/weaveworks/ignite/pkg/apis/meta/v1alpha1"
	"github.com/weaveworks/ignite/pkg/constants"
	"github.com/weaveworks/ignite/pkg/storage/serializer"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

// Storage is an interface for persisting and retrieving API objects to/from a backend
// One Storage instance handles all different Kinds of Objects
type Storage interface {
	// Get populates the Object using the given pointer, based on the file content
	Get(obj meta.Object) error
	// Set saves the Object to disk. If the object does not exist, the
	// ObjectMeta.Created field is set automatically
	Set(obj meta.Object) error
	// GetByID returns a new Object for the resource at the specified kind/uid path, based on the file content
	GetByID(kind meta.Kind, uid meta.UID) (meta.Object, error)
	// Delete removes an object from the storage
	Delete(kind meta.Kind, uid meta.UID) error
	// List lists objects for the specific kind
	List(kind meta.Kind) ([]meta.Object, error)
	// ListMeta lists all objects' APIType representation. In other words,
	// only metadata about each object is unmarshalled (uid/name/kind/apiVersion).
	// This allows for faster runs (no need to unmarshal "the world"), and less
	// resource usage, when only metadata is unmarshalled into memory
	ListMeta(kind meta.Kind) (meta.APITypeList, error)
	// Count returns the amount of available Objects of a specific kind
	// This is used by Caches to check if all objects are cached to perform a List
	Count(kind meta.Kind) (uint64, error)
	// Find and FindAll are used to match single or multiple Objects
	// of a given type based on the given filter
	Find(kind meta.Kind, filter Filter) (meta.Object, error)
	FindAll(kind meta.Kind, filter Filter) ([]meta.Object, error)
}

// DefaultStorage is the default storage impl
var DefaultStorage = NewGenericStorage(NewDefaultRawStorage(constants.DATA_DIR), scheme.Serializer)

// NewGenericStorage constructs a new Storage
func NewGenericStorage(rawStorage RawStorage, serializer serializer.Serializer) Storage {
	return &GenericStorage{rawStorage, serializer}
}

// GenericStorage implements the Storage interface
type GenericStorage struct {
	raw        RawStorage
	serializer serializer.Serializer
}

var _ Storage = &GenericStorage{}

// Get populates the pointer to the Object given, based on the file content
func (s *GenericStorage) Get(obj meta.Object) error {
	storageKey, err := s.keyForObj(obj)
	if err != nil {
		return err
	}
	content, err := s.raw.Read(storageKey)
	if err != nil {
		return err
	}
	return s.serializer.DecodeInto(content, obj)
}

// GetByID returns a new Object for the resource at the specified kind/uid path, based on the file content
func (s *GenericStorage) GetByID(kind meta.Kind, uid meta.UID) (meta.Object, error) {
	storageKey := s.keyForID(kind, uid.String())
	content, err := s.raw.Read(storageKey)
	if err != nil {
		return nil, err
	}
	obj, err := s.serializer.Decode(content)
	if err != nil {
		return nil, err
	}
	igniteObj, ok := obj.(meta.Object)
	if !ok {
		return nil, fmt.Errorf("cannot convert ignite Object")
	}
	return igniteObj, nil
}

// Set saves the Object to disk. If the object does not exist, the
// ObjectMeta.Created field is set automatically
func (s *GenericStorage) Set(obj meta.Object) error {
	storageKey, err := s.keyForObj(obj)
	if err != nil {
		return err
	}
	if !s.raw.Exists(storageKey) {
		// Register that the object was created now
		ts := meta.Timestamp()
		obj.SetCreated(&ts)
	}

	b, err := s.serializer.EncodeJSON(obj)
	if err != nil {
		return err
	}
	return s.raw.Write(storageKey, b)
}

// Delete removes an object from the storage
func (s *GenericStorage) Delete(kind meta.Kind, uid meta.UID) error {
	storageKey := s.keyForID(kind, uid.String())
	return s.raw.Delete(storageKey)
}

// List lists objects for the specific kind
func (s *GenericStorage) List(kind meta.Kind) ([]meta.Object, error) {
	result := []meta.Object{}
	err := s.walkKind(kind, func(content []byte) error {
		runtimeobj, err := s.serializer.Decode(content)
		if err != nil {
			return err
		}
		obj, ok := runtimeobj.(meta.Object)
		if !ok {
			return fmt.Errorf("can't convert to ignite object")
		}
		result = append(result, obj)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ListMeta lists all objects' APIType representation. In other words,
// only metadata about each object is unmarshalled (uid/name/kind/apiVersion).
// This allows for faster runs (no need to unmarshal "the world"), and less
// resource usage, when only metadata is unmarshalled into memory
func (s *GenericStorage) ListMeta(kind meta.Kind) (meta.APITypeList, error) {
	result := meta.APITypeList{}
	err := s.walkKind(kind, func(content []byte) error {
		obj := &meta.APIType{}
		// The yaml package supports both YAML and JSON
		if err := yaml.Unmarshal(content, obj); err != nil {
			return err
		}
		result = append(result, obj)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Count counts the objects for the specific kind
func (s *GenericStorage) Count(kind meta.Kind) (uint64, error) {
	entries, err := s.raw.List(s.keyForKind(kind))
	return uint64(len(entries)), err
}

func (c *GenericStorage) Find(kind meta.Kind, filter Filter) (meta.Object, error) {
	// TODO: Find implementation
	var objects []meta.Object
	var err error

	if filter.Meta() {
		objects, err = c.ListMeta(kind)
	} else {

	}

	if err != nil {
		return nil, err
	}
}

func (c *GenericStorage) FindAll(kind meta.Kind, filter Filter) ([]meta.Object, error) {
	// TODO: FindAll implementation
}

func (s *GenericStorage) walkKind(kind meta.Kind, fn func(content []byte) error) error {
	kindKey := s.keyForKind(kind)
	entries, err := s.raw.List(kindKey)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// Allow metadata.json to not exist, although the directory does exist
		if !s.raw.Exists(entry) {
			continue
		}

		content, err := s.raw.Read(entry)
		if err != nil {
			return err
		}
		if err := fn(content); err != nil {
			return err
		}
	}
	return nil
}

func (s *GenericStorage) keyForObj(obj meta.Object) (string, error) {
	gvk, err := s.gvkFromObj(obj)
	if err != nil {
		return "", err
	}
	return s.keyForID(meta.Kind(gvk.Kind), obj.GetUID().String()), nil
}

func (s *GenericStorage) keyForID(kind meta.Kind, uid string) string {
	return "/" + path.Join(kind.String(), uid)
}

func (s *GenericStorage) keyForKind(kind meta.Kind) string {
	return "/" + kind.String()
}

func (s *GenericStorage) gvkFromObj(obj runtime.Object) (*schema.GroupVersionKind, error) {
	gvks, unversioned, err := s.serializer.Scheme().ObjectKinds(obj.(runtime.Object))
	if err != nil {
		return nil, err
	}
	if unversioned {
		return nil, fmt.Errorf("unversioned")
	}
	if len(gvks) == 0 {
		return nil, fmt.Errorf("unexpected gvks")
	}
	return &gvks[0], nil
}
