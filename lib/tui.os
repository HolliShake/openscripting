/**
 * Small terminal UI helpers for OpenScript. Internal implementation names
 * use a double-underscore prefix and are not importable from other modules.
 * Import named functions from "lib:tui".
 * ANSI styling can be disabled by ignoring the color helpers in plain terminals.
 */

import { print, println, scan } from "core:io";
import { stringify } from "core:json";
import { length } from "core:unicode";
import { floor } from "core:math";

const __reset = "\x1b[0m";
const __boldCode = "\x1b[1m";
const __dimCode = "\x1b[2m";
const __redCode = "\x1b[31m";
const __greenCode = "\x1b[32m";
const __yellowCode = "\x1b[33m";
const __blueCode = "\x1b[34m";
const __cyanCode = "\x1b[36m";

var __style::fn(text, code) {
    return code + text + __reset;
};

var bold::fn(text) {
    return __style(text, __boldCode);
};

var dim::fn(text) {
    return __style(text, __dimCode);
};

var red::fn(text) {
    return __style(text, __redCode);
};

var green::fn(text) {
    return __style(text, __greenCode);
};

var yellow::fn(text) {
    return __style(text, __yellowCode);
};

var blue::fn(text) {
    return __style(text, __blueCode);
};

var cyan::fn(text) {
    return __style(text, __cyanCode);
};

var success::fn(message) {
    println(green("[OK] ") + message);
};

var warning::fn(message) {
    println(yellow("[!] ") + message);
};

var error::fn(message) {
    println(red("[ERROR] ") + message);
};

var info::fn(message) {
    println(cyan("[INFO] ") + message);
};

var __repeat::fn(character, count) {
    let result = "";
    loop (let index = 0; index < count; index++) {
        result += character;
    }
    return result;
};

var divider::fn(width) {
    println(dim(__repeat("-", width)));
};

var clear::fn() {
    print("\x1b[2J\x1b[H");
};

var prompt::fn(label) {
    return scan(label);
};

var confirm::fn(question) {
    let answer = scan(question + " (y/N): ");
    if (answer == null) {
        return false;
    }
    answer = answer->trim()->toLower();
    return answer == "y" || answer == "yes";
};

var __cellText::fn(value) {
    if (typeof value == "string") {
        return value;
    }
    return stringify(value);
};

var __padRight::fn(value, width) {
    return value + __repeat(" ", width - length(value));
};

var __padCenter::fn(value, width) {
    let padding = width - length(value);
    let left = floor(padding / 2);
    let right = padding - left;
    return __repeat(" ", left) + value + __repeat(" ", right);
};

var __columnWidth::fn(arrayOfObject, columns, columnIndex) {
    let width = length(columns[columnIndex]);
    loop (let rowIndex = 0; rowIndex < arrayOfObject->len(); rowIndex++) {
        let value = __cellText(arrayOfObject[rowIndex][columns[columnIndex]]);
        if (length(value) > width) {
            width = length(value);
        }
    }
    return width;
};

var assertSameKeys::fn(arrayOfObject) {
    if (typeof arrayOfObject != "array") {
        return false;
    }
    if (arrayOfObject->len() < 2) {
        return true;
    }

    let expectedKeys = arrayOfObject[0]->keys();
    loop (let rowIndex = 1; rowIndex < arrayOfObject->len(); rowIndex++) {
        let row = arrayOfObject[rowIndex];
        if (typeof row != "object") {
            return false;
        }
        let rowKeys = row->keys();
        if (rowKeys->len() != expectedKeys->len()) {
            return false;
        }
        loop (let keyIndex = 0; keyIndex < expectedKeys->len(); keyIndex++) {
            if (!rowKeys->contains(expectedKeys[keyIndex])) {
                return false;
            }
        }
    }
    return true;
};

var printTable::fn(arrayOfObject) {
    if (arrayOfObject->len() == 0) {
        println(dim("(no data)"));
        return null;
    }
    if (!assertSameKeys(arrayOfObject)) {
        error("printTable expects objects with identical keys");
        return null;
    }

    let columns = arrayOfObject[0]->keys();
    if (columns->len() == 0) {
        println(dim("(no columns)"));
        return null;
    }

    let border = "+";
    loop (let columnIndex = 0; columnIndex < columns->len(); columnIndex++) {
        border += __repeat("-", __columnWidth(arrayOfObject, columns, columnIndex) + 2) + "+";
    }
    println(border);

    let header = "|";
    loop (let columnIndex = 0; columnIndex < columns->len(); columnIndex++) {
        header += " " + __padRight(columns[columnIndex], __columnWidth(arrayOfObject, columns, columnIndex)) + " |";
    }
    println(bold(header));
    println(border);

    loop (let rowIndex = 0; rowIndex < arrayOfObject->len(); rowIndex++) {
        let row = arrayOfObject[rowIndex];
        let line = "|";
        loop (let columnIndex = 0; columnIndex < columns->len(); columnIndex++) {
            let value = __cellText(row[columns[columnIndex]]);
            line += " " + __padCenter(value, __columnWidth(arrayOfObject, columns, columnIndex)) + " |";
        }
        println(line);
    }
    println(border);
    return null;
};