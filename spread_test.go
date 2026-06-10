package otto

import (
	"testing"
)

func TestSpreadCall(t *testing.T) {
	tt(t, func() {
		test, _ := test()

		test(`
            function f(a, b, c) { return a + b + c; }
            f(...[1, 2, 3]);
        `, 6)

		// Spread mixed with fixed arguments.
		test(`
            function f(a, b, c, d) { return [a, b, c, d].join(","); }
            f(1, ...[2, 3], 4);
        `, "1,2,3,4")

		// Spread into a built-in.
		test(`Math.max(...[4, 1, 9, 2]);`, 9)

		// Spread feeding a rest parameter.
		test(`
            function f(x, ...r) { return x + ":" + r.join(","); }
            f(...[1, 2, 3, 4]);
        `, "1:2,3,4")
	})
}

func TestSpreadArrayLiteral(t *testing.T) {
	tt(t, func() {
		test, _ := test()

		test(`
            var a = [1, 2];
            [0, ...a, 3].join(",");
        `, "0,1,2,3")

		test(`
            var a = [1, 2], b = [3, 4];
            [...a, ...b].length;
        `, 4)

		// Spreading a string iterates its characters.
		test(`[..."abc"].join("-");`, "a-b-c")

		// Spreading an array-like object.
		test(`
            var o = { 0: "x", 1: "y", length: 2 };
            [...o].join(",");
        `, "x,y")
	})
}

func TestSpreadNewExpression(t *testing.T) {
	tt(t, func() {
		test, _ := test()

		test(`
            function Point(x, y) { this.x = x; this.y = y; }
            var p = new Point(...[1, 2]);
            p.x + "," + p.y;
        `, "1,2")

		// Spread mixed with fixed arguments.
		test(`
            function T(a, b, c) { this.s = [a, b, c].join(","); }
            new T(1, ...[2, 3]).s;
        `, "1,2,3")

		// Spread into a built-in constructor.
		test(`new Array(...[1, 2, 3]).join(",");`, "1,2,3")

		// Spreading a non-iterable into new throws a TypeError.
		test(`
            try {
                new Date(...5);
            } catch (e) {
                e instanceof TypeError;
            }
        `, true)
	})
}
