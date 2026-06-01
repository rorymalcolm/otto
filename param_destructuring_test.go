package otto

import (
	"testing"
)

func TestObjectParameterDestructuring(t *testing.T) {
	tt(t, func() {
		test, _ := test()

		test(`
            function f({a, b}) { return a + b; }
            f({a: 1, b: 2});
        `, 3)

		// Default within an object pattern parameter.
		test(`
            function f({a, b = 10}) { return a + b; }
            f({a: 5});
        `, 15)

		// Nested object pattern.
		test(`
            function f({a: {b}}) { return b; }
            f({a: {b: 42}});
        `, 42)

		// A defaulted object-pattern parameter.
		test(`
            function f({a = 1, b = 2} = {}) { return a + "/" + b; }
            f();
        `, "1/2")

		// Mixed plain and pattern parameters.
		test(`
            function f(x, {y, z}) { return x + y + z; }
            f(1, {y: 2, z: 3});
        `, 6)
	})
}

func TestArrayParameterDestructuring(t *testing.T) {
	tt(t, func() {
		test, _ := test()

		test(`
            function f([x, y]) { return x * y; }
            f([3, 4]);
        `, 12)

		// Rest within an array pattern parameter.
		test(`
            function f([a, ...rest]) { return a + "/" + rest.join(","); }
            f([1, 2, 3]);
        `, "1/2,3")
	})
}

func TestArrowParameterDestructuring(t *testing.T) {
	tt(t, func() {
		test, _ := test()

		test(`var g = ({x, y}) => x + y; g({x: 7, y: 8});`, 15)
		test(`var h = ([a, b]) => a + "-" + b; h(["p", "q"]);`, "p-q")

		// Cover grammar: defaulted object pattern in an arrow parameter.
		test(`var k = ({n = 3} = {}) => n; [k(), k({n: 9})].join(",");`, "3,9")

		// A destructuring callback, the common real-world use.
		test(`
            [{a: 1, b: 2}, {a: 3, b: 4}].map(({a, b}) => a + b).join(",");
        `, "3,7")
	})
}
