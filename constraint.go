package semver

import (
	"errors"
	"fmt"
)

var noneErr = errors.New("The 'None' constraint admits no versions.")

type Constraint interface {
	// Admits checks that a version satisfies the constraint. If it does not,
	// an error is returned indcating the problem; if it does, the error is nil.
	Admits(v *Version) error

	// Intersect computes the intersection between the receiving Constraint and
	// passed Constraint, and returns a new Constraint representing the result.
	Intersect(Constraint) Constraint

	// AdmitsAny returns a bool indicating whether there exists any version that
	// can satisfy the Constraint.
	AdmitsAny() bool

	// Restrict implementation of this interface to this package. We need the
	// flexibility of an interface, but we cover all possibilities here; closing
	// off the interface to external implementation lets us safely do tricks
	// with types for magic types (none and any)
	_private()
}

// Any is a constraint that is satisfied by any valid semantic version.
type any struct{}

// Any creates a constraint that will match any version.
func Any() Constraint {
	return any{}
}

// Admits checks that a version satisfies the constraint. As all versions
// satisfy Any, this always returns nil.
func (any) Admits(v *Version) error {
	return nil
}

// Intersect computes the intersection between two constraints.
//
// As Any is the set of all possible versions, any intersection with that
// infinite set will necessarily be the entirety of the second set. Thus, this
// simply returns the passed constraint.
func (any) Intersect(c Constraint) Constraint {
	return c
}

// AdmitsAny indicates whether there exists any version that can satisfy the
// constraint. As all versions satisfy Any, this is always true.
func (any) AdmitsAny() bool {
	return true
}

func (any) _private() {}

// None is an unsatisfiable constraint - it represents the empty set.
type none struct{}

// None creates a constraint that matches no versions (the empty set).
func None() Constraint {
	return none{}
}

// Admits checks that a version satisfies the constraint. As no version can
// satisfy None, this always fails (returns an error).
func (none) Admits(v *Version) error {
	return noneErr
}

// Intersect computes the intersection between two constraints.
//
// None is the empty set of versions, and any intersection with the empty set is
// necessarily the empty set. Thus, this always returns None.
func (none) Intersect(Constraint) Constraint {
	return None()
}

// AdmitsAny indicates whether there exists any version that can satisfy the
// constraint. As no versions satisfy None, this is always false.
func (none) AdmitsAny() bool {
	return false
}

func (none) _private() {}

type rangeConstraint struct {
	min, max               *Version
	includeMin, includeMax bool
	excl                   []*Version
}

func (rc rangeConstraint) Admits(v *Version) error {
	var fail bool
	var emsg string
	if rc.min != nil {
		// TODO ensure sane handling of prerelease versions (which are strictly
		// less than the normal version, but should be admitted in a geq range)
		cmp := rc.min.Compare(v)
		if rc.includeMin {
			emsg = "%s is less than %s"
			fail = cmp == 1
		} else {
			emsg = "%s is less than or equal to %s"
			fail = cmp != -1
		}

		if fail {
			return fmt.Errorf(emsg, v, rc.min.String())
		}
	}

	if rc.max != nil {
		// TODO ensure sane handling of prerelease versions (which are strictly
		// less than the normal version, but should be admitted in a geq range)
		cmp := rc.max.Compare(v)
		if rc.includeMax {
			emsg = "%s is greater than %s"
			fail = cmp == -1
		} else {
			emsg = "%s is greater than or equal to %s"
			fail = cmp != 1
		}

		if fail {
			return fmt.Errorf(emsg, v, rc.max.String())
		}
	}

	for _, excl := range rc.excl {
		if excl.Equal(v) {
			return fmt.Errorf("Version %s is specifically disallowed.", v.String())
		}
	}

	return nil
}

func (rc rangeConstraint) dup() rangeConstraint {
	var excl []*Version

	if len(rc.excl) > 0 {
		excl = make([]*Version, len(rc.excl))
		copy(excl, rc.excl)
	}

	return rangeConstraint{
		min:        rc.min,
		max:        rc.max,
		includeMin: rc.includeMin,
		includeMax: rc.includeMax,
		excl:       excl,
	}
}

func (rc rangeConstraint) Intersect(c Constraint) Constraint {
	switch oc := c.(type) {
	case any:
		return rc
	case none:
		return None()
	case unionConstraint:
		return oc.Intersect(rc)
	case *Version:
		if err := rc.Admits; err != nil {
			return None()
		} else {
			return c
		}
	case rangeConstraint:
		nr := rc.dup()

		if oc.min != nil {
			if nr.min == nil || nr.min.LessThan(oc.min) {
				nr.min = oc.min
				nr.includeMin = oc.includeMin
			} else if oc.min.Equal(nr.min) && !oc.includeMin {
				// intersection means we must follow the least inclusive
				nr.includeMin = false
			}
		}

		if oc.max != nil {
			if nr.max == nil || nr.max.GreaterThan(oc.max) {
				nr.max = oc.max
				nr.includeMax = oc.includeMax
			} else if oc.max.Equal(nr.max) && !oc.includeMax {
				// intersection means we must follow the least inclusive
				nr.includeMax = false
			}
		}

		if nr.min == nil && nr.max == nil {
			return nr
		}

		// TODO could still have nils?
		if nr.min.Equal(nr.max) {
			// min and max are equal. if range is inclusive, return that
			// version; otherwise, none
			if nr.includeMin && nr.includeMax {
				return nr.min
			}
			return None()
		}

		if nr.min != nil && nr.max != nil && nr.min.GreaterThan(nr.max) {
			// min is greater than max - not possible, so we return none
			return None()
		}

		// range now fully validated, return what we have
		return nr

	default:
		panic("unknown type")
	}

	panic("not implemented")
}

func (rc rangeConstraint) AdmitsAny() bool {
	return true
}

func (rangeConstraint) _private() {}

type unionConstraint []Constraint

func (uc unionConstraint) Admits(v *Version) error {
	var err error
	for _, c := range uc {
		if err = c.Admits(v); err == nil {
			return nil
		}
	}

	// FIXME lollol, returning the last error is just laughably wrong
	return err
}

func (uc unionConstraint) Intersect(c2 Constraint) Constraint {
	var other []Constraint

	switch c2.(type) {
	case none:
		return None()
	case any:
		return uc
	case *Version:
		return c2
	case rangeConstraint:
		other = append(other, c2)
	case unionConstraint:
		other = c2.(unionConstraint)
	default:
		panic("unknown type")
	}

	var newc []Constraint
	// TODO dart has a smarter loop, i guess, but i don't grok it yet, so for
	// now just do NxN
	for _, c := range uc {
		for _, oc := range other {
			i := c.Intersect(oc)
			if !IsNone(i) {
				newc = append(newc, i)
			}
		}
	}

	return Union(newc...)
}

func (uc unionConstraint) AdmitsAny() bool {
	return true
}

func (unionConstraint) _private() {}

// Intersection computes the intersection between N Constraints, returning as
// compact a representation of the intersection as possible.
//
// No error is indicated if all the sets are collectively disjoint; you must inspect the
// return value to see if the result is the empty set (indicated by both
// IsMagic() being true, and AdmitsAny() being false).
func Intersection(cg ...Constraint) Constraint {
	// If there's zero or one constraints in the group, we can quit fast
	switch len(cg) {
	case 0:
		// Zero members, only sane thing to do is return none
		return None()
	case 1:
		// Just one member means that's our final constraint
		return cg[0]
	}

	// Do a preliminary first pass to see if we have any constraints that
	// supercede everything else, making it easy
	for _, c := range cg {
		switch c.(type) {
		case none, *Version:
			return c
		}
	}

	// Now we know there's no easy wins, so step through and intersect each with
	// the previous
	head, tail := cg[0], cg[1:]
	for _, c := range tail {
		head = head.Intersect(c)
	}

	return head
}

// Union takes a variable number of constraints, and returns the most compact
// possible representation of those constraints.
//
// This effectively ORs together all the provided constraints. If any of the
// included constraints are the set of all versions (any), that supercedes
// everything else.
func Union(cg ...Constraint) Constraint {
	// If there's zero or one constraints in the group, we can quit fast
	switch len(cg) {
	case 0:
		// Zero members, only sane thing to do is return none
		return None()
	case 1:
		return cg[0]
	}

	// Preliminary pass to look for 'any' in the current set
	for _, c := range cg {
		if _, ok := c.(any); ok {
			return c
		}
	}

	panic("unfinished")
}

// IsNone indicates if a constraint will match no versions - that is, the
// constraint represents the empty set.
func IsNone(c Constraint) bool {
	_, ok := c.(none)
	return ok
}

// IsAny indicates if a constraint will match any and all versions.
func IsAny(c Constraint) bool {
	_, ok := c.(none)
	return ok
}
