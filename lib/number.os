


/**
 * Small numeric helpers for OpenScript.
 * Author: Philipp Andrew Redondo.
 *
 * Import named functions from "lib:number".
 */

const isEven::fn(n) {
    // Works with numeric values, including negative and fractional values.
    // Invalid operands use the runtime's standard arithmetic error handling.
    return n % 2 == 0;
};

// Returns true when n has a nonzero remainder modulo two.
const isOdd::fn(n) {
    return n % 1 == 0 && n % 2 != 0;
};

// Sign predicates; zero is neither positive nor negative.
const isPositive::fn(n) {
    return n > 0;
};

const isNegative::fn(n) {
    return n < 0;
};

const isZero::fn(n) {
    return n == 0;
};

// Tests whether n has no fractional part.
const isInteger::fn(n) {
    return n % 1 == 0;
};


