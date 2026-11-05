/**
 * Command-line argument parsing helpers. Internal implementation functions
 * use a double-underscore prefix and are not importable from other modules.
 * Author: Philipp Andrew Redondo.
 *
 * The primary entry point is ArgumentParser(config?). Configuration accepts
 * prog, description, allow_abbrev, and add_help. The parser reads core:sys.argv
 * when parse_args() or parse_known_args() is called without an argument array.
 *
 * Register positional arguments with add_argument({name?, dest?, ...}) and
 * options with add_argument({flags: [...], dest?, ...}). Definitions may use
 * help, metavar, default, type, choices, nargs, required, action, and const.
 * Supported types are string/str, int/integer, float, and bool/boolean.
 * nargs accepts '?', '*', '+', or a fixed number. Supported actions are
 * store, append, store_true, store_false, store_const, append_const, count,
 * and help. Options support --name=value, short-option clusters, and long
 * option abbreviation when allow_abbrev is enabled.
 *
 * parse_args(args?) returns a namespace object, or an object with an error
 * field on failure. The built-in help action returns help text in a help field.
 * parse_known_args(args?) returns {namespace, unknown}, preserving unrecognized
 * option tokens and extra positional tokens in unknown. Argument errors are
 * returned as values rather than thrown. ArgumentError(message) constructs an
 * object with an error field.
 */

import { argv } from "core:sys";
import { println } from "core:io";
import { parseInt, parseFloat } from "core:number";
import { length, codePointAt, fromCodePoint } from "core:unicode";

var __makeError::fn(message) {
	return {error: message};
};

var __substring::fn(value, start) {
	let output = "";
	loop (let index = start; index < length(value); index++) {
		output += fromCodePoint(codePointAt(value, index));
	}
	return output;
};

var __splitOption::fn(token) {
	let name = "";
	let value = "";
	let hasValue = false;
	let rightSide = false;
	loop (let index = 0; index < length(token); index++) {
		let point = codePointAt(token, index);
		if (point == 61 && !rightSide) {
			rightSide = true;
			hasValue = true;
		} else if (rightSide) {
			value += fromCodePoint(point);
		} else {
			name += fromCodePoint(point);
		}
	}
	return {name: name, value: value, hasValue: hasValue};
};

var __startsWith::fn(value, prefix) {
	if (length(prefix) > length(value)) {
		return false;
	}
	loop (let index = 0; index < length(prefix); index++) {
		if (codePointAt(value, index) != codePointAt(prefix, index)) {
			return false;
		}
	}
	return true;
};

var __containsSpace::fn(value) {
	loop (let index = 0; index < length(value); index++) {
		if (codePointAt(value, index) == 32) {
			return true;
		}
	}
	return false;
};

// "-5", "-1.5", "-.5"
var __isNegativeNumber::fn(token) {
	if (length(token) < 2 || codePointAt(token, 0) != 45) {
		return false;
	}
	let digits = 0;
	let dots = 0;
	loop (let index = 1; index < length(token); index++) {
		let point = codePointAt(token, index);
		if (point >= 48 && point <= 57) {
			digits++;
		} else if (point == 46 && dots == 0) {
			dots++;
		} else {
			return false;
		}
	}
	return digits > 0;
};

var __findExact::fn(parser, name) {
	loop (let specIndex = 0; specIndex < parser->options->length; specIndex++) {
		let spec = parser->options[specIndex];
		loop (let nameIndex = 0; nameIndex < spec->flags->length; nameIndex++) {
			if (spec->flags[nameIndex] == name) {
				return spec;
			}
		}
	}
	return null;
};

var __hasNegativeLookingOption::fn(parser) {
	loop (let specIndex = 0; specIndex < parser->options->length; specIndex++) {
		let spec = parser->options[specIndex];
		loop (let nameIndex = 0; nameIndex < spec->flags->length; nameIndex++) {
			if (__isNegativeNumber(spec->flags[nameIndex])) {
				return true;
			}
		}
	}
	return false;
};

var __isOptionToken::fn(parser, token) {
	if (length(token) < 2 || codePointAt(token, 0) != 45) {
		return false;
	}
	if (token == "--") {
		return true;
	}
	let split = __splitOption(token);
	if (__findExact(parser, split->name) != null) {
		return true;
	}
	if (__isNegativeNumber(token)) {
		return __hasNegativeLookingOption(parser);
	}
	if (__containsSpace(token)) {
		return false;
	}
	return true;
};

var __actionOf::fn(spec) {
	if (spec->action == null) {
		return "store";
	}
	return spec->action;
};

var __takesValue::fn(spec) {
	let action = __actionOf(spec);
	return action == "store" || action == "append";
};

var __deriveDest::fn(flags) {
	let chosen = flags[0];
	loop (let index = 0; index < flags->length; index++) {
		if (__startsWith(flags[index], "--")) {
			chosen = flags[index];
			break;
		}
	}
	let output = "";
	loop (let index = 0; index < length(chosen); index++) {
		let point = codePointAt(chosen, index);
		if (point == 45 && output == "") {
			continue;
		}
		if (point == 45) {
			output += "_";
		} else {
			output += fromCodePoint(point);
		}
	}
	return output;
};

var __convertValue::fn(value, valueType) {
	if (valueType == null || valueType == "string" || valueType == "str") {
		return {value: value, error: null};
	}
	if (valueType == "int" || valueType == "integer") {
		let converted = parseInt(value);
		if (converted == null) {
			return {value: null, error: "invalid int value: '" + value + "'"};
		}
		return {value: converted, error: null};
	}
	if (valueType == "float") {
		let converted = parseFloat(value);
		if (converted == null) {
			return {value: null, error: "invalid float value: '" + value + "'"};
		}
		return {value: converted, error: null};
	}
	if (valueType == "bool" || valueType == "boolean") {
		if (value == "true" || value == "1") {
			return {value: true, error: null};
		}
		if (value == "false" || value == "0") {
			return {value: false, error: null};
		}
		return {value: null, error: "invalid bool value: '" + value + "'"};
	}
	return {value: null, error: "unsupported argument type: " + valueType};
};

var __checkChoices::fn(spec, value) {
	if (spec->choices == null) {
		return null;
	}
	let list = "";
	loop (let index = 0; index < spec->choices->length; index++) {
		if (value == spec->choices[index]) {
			return null;
		}
		if (index > 0) {
			list += ", ";
		}
		list += "'" + spec->choices[index] + "'";
	}
	return "invalid choice: '" + value + "' (choose from " + list + ")";
};

// Convert + validate a list of raw tokens for a spec. Returns {values, error}.
var __convertAll::fn(spec, raw) {
	let out = [];
	loop (let index = 0; index < raw->length; index++) {
		let converted = __convertValue(raw[index], spec->type);
		if (converted->error != null) {
			return {values: null, error: converted->error};
		}
		let choiceError = __checkChoices(spec, converted->value);
		if (choiceError != null) {
			return {values: null, error: choiceError};
		}
		out = [...out, converted->value];
	}
	return {values: out, error: null};
};

var __matchOption::fn(parser, token) {
	let exact = __findExact(parser, token);
	if (exact != null) {
		return {spec: exact, ambiguous: false};
	}
	let abbreviation = null;
	let ambiguous = false;
	if (parser->allow_abbrev && length(token) > 2 && __startsWith(token, "--")) {
		loop (let specIndex = 0; specIndex < parser->options->length; specIndex++) {
			let spec = parser->options[specIndex];
			loop (let nameIndex = 0; nameIndex < spec->flags->length; nameIndex++) {
				let flag = spec->flags[nameIndex];
				if (__startsWith(flag, token)) {
					if (abbreviation != null && abbreviation->dest != spec->dest) {
						ambiguous = true;
					}
					abbreviation = spec;
				}
			}
		}
	}
	return {spec: abbreviation, ambiguous: ambiguous};
};

var __positionalLabel::fn(positional) {
	if (positional->metavar != null) {
		return positional->metavar;
	}
	return positional->dest;
};

var __formatHelp::fn(parser) {
	let usage = "usage: " + parser->prog;
	if (parser->options->length > 0) {
		usage += " [options]";
	}
	loop (let index = 0; index < parser->positionals->length; index++) {
		usage += " " + __positionalLabel(parser->positionals[index]);
	}
	let output = usage + "\n";
	if (parser->description != "") {
		output += "\n" + parser->description + "\n";
	}
	if (parser->positionals->length > 0) {
		output += "\npositional arguments:\n";
		loop (let index = 0; index < parser->positionals->length; index++) {
			let positional = parser->positionals[index];
			output += "  " + __positionalLabel(positional);
			if (positional->help != null) {
				output += "  " + positional->help;
			}
			output += "\n";
		}
	}
	if (parser->options->length > 0) {
		output += "\noptions:\n";
		loop (let index = 0; index < parser->options->length; index++) {
			let spec = parser->options[index];
			let flags = "";
			loop (let flagIndex = 0; flagIndex < spec->flags->length; flagIndex++) {
				if (flagIndex > 0) {
					flags += ", ";
				}
				flags += spec->flags[flagIndex];
			}
			if (__takesValue(spec)) {
				let metavar = spec->metavar;
				if (metavar == null) {
					metavar = "VALUE";
				}
				flags += " " + metavar;
			}
			output += "  " + flags;
			if (spec->help != null) {
				output += "  " + spec->help;
			}
			output += "\n";
		}
	}
	return output;
};

var __minCount::fn(positional) {
	let nargs = positional->nargs;
	if (nargs == "?" || nargs == "*") {
		return 0;
	}
	if (nargs == "+" || nargs == null) {
		return 1;
	}
	return nargs;
};

// Distribute collected positional tokens across positional specs.
var __assignPositionals::fn(parser, tokens, namespace) {
	let positionals = parser->positionals;
	let used = 0;
	let missing = [];
	loop (let index = 0; index < positionals->length; index++) {
		let positional = positionals[index];
		let nargs = positional->nargs;
		let reserve = 0;
		loop (let later = index + 1; later < positionals->length; later++) {
			reserve += __minCount(positionals[later]);
		}
		let avail = tokens->length - used - reserve;
		if (avail < 0) {
			avail = 0;
		}
		let take = 0;
		let failed = false;
		if (nargs == null) {
			if (avail >= 1) { take = 1; } else { failed = true; }
		} else if (nargs == "?") {
			if (avail >= 1) { take = 1; }
		} else if (nargs == "*") {
			take = avail;
		} else if (nargs == "+") {
			if (avail >= 1) { take = avail; } else { failed = true; }
		} else {
			if (avail >= nargs) { take = nargs; } else { failed = true; }
		}
		if (failed) {
			missing = [...missing, __positionalLabel(positional)];
			continue;
		}
		let raw = [];
		loop (let offset = 0; offset < take; offset++) {
			raw = [...raw, tokens[used + offset]];
		}
		used += take;
		let converted = __convertAll(positional, raw);
		if (converted->error != null) {
			return {error: converted->error};
		}
		let isList = nargs != null && nargs != "?";
		if (take == 0) {
			if (nargs == "*" && positional["default"] == null) {
				namespace[positional->dest] = [];
			}
			continue;
		}
		if (isList) {
			namespace[positional->dest] = converted->values;
		} else {
			namespace[positional->dest] = converted->values[0];
		}
	}
	let extras = [];
	loop (let index = used; index < tokens->length; index++) {
		extras = [...extras, tokens[index]];
	}
	return {error: null, missing: missing, extras: extras};
};

var __joinWords::fn(words, separator) {
	let out = "";
	loop (let index = 0; index < words->length; index++) {
		if (index > 0) {
			out += separator;
		}
		out += words[index];
	}
	return out;
};

var __parseInternal::fn(parser, input, allowUnknown) {
	if (input == null) {
		input = argv;
	}
	let namespace = {};
	let seen = {};
	let unknown = [];
	let positionalTokens = [];
	let positionalOnly = false;
	let pending = null;

	loop (let specIndex = 0; specIndex < parser->options->length; specIndex++) {
		let spec = parser->options[specIndex];
		let action = __actionOf(spec);
		if (action == "help") {
			continue;
		}
		let defaultValue = spec["default"];
		if (defaultValue == null) {
			if (action == "store_true") {
				defaultValue = false;
			} else if (action == "store_false") {
				defaultValue = true;
			} else if (action == "append" || action == "append_const") {
				defaultValue = [];
			}
		}
		namespace[spec->dest] = defaultValue;
	}
	loop (let specIndex = 0; specIndex < parser->positionals->length; specIndex++) {
		let spec = parser->positionals[specIndex];
		namespace[spec->dest] = spec["default"];
	}

	loop (let argIndex = 0; argIndex < input->length;) {
		let token = input[argIndex];
		let clustered = false;
		if (pending != null) {
			token = pending;
			pending = null;
			clustered = true;
		}
		if (!clustered && !positionalOnly && token == "--") {
			positionalOnly = true;
			argIndex++;
			continue;
		}

		let split = __splitOption(token);
		let spec = null;
		if (!positionalOnly && __isOptionToken(parser, token)) {
			let match = __matchOption(parser, split->name);
			if (match->ambiguous) {
				return __makeError("ambiguous option: " + split->name + " could match multiple options");
			}
			spec = match->spec;
			// short option with attached value or cluster: -n5, -abc
			if (spec == null && length(split->name) > 2 && codePointAt(split->name, 1) != 45) {
				let first = "-" + fromCodePoint(codePointAt(split->name, 1));
				let shortSpec = __findExact(parser, first);
				if (shortSpec != null) {
					spec = shortSpec;
					let rest = __substring(token, 2);
					if (__takesValue(shortSpec)) {
						split = {name: first, value: rest, hasValue: true};
					} else {
						split = {name: first, value: "", hasValue: false};
						pending = "-" + rest;
					}
				}
			}
		}

		if (spec != null) {
			let action = __actionOf(spec);
			seen[spec->dest] = true;
			if (action == "help") {
				return {help: __formatHelp(parser), namespace: namespace};
			}
			if (!__takesValue(spec)) {
				if (split->hasValue) {
					return __makeError("argument " + split->name + ": ignored explicit argument '" + split->value + "'");
				}
				if (action == "store_true") {
					namespace[spec->dest] = true;
				} else if (action == "store_false") {
					namespace[spec->dest] = false;
				} else if (action == "store_const") {
					namespace[spec->dest] = spec["const"];
				} else if (action == "append_const") {
					namespace[spec->dest] = [...namespace[spec->dest], spec["const"]];
				} else if (action == "count") {
					if (namespace[spec->dest] == null) {
						namespace[spec->dest] = 0;
					}
					namespace[spec->dest]++;
				} else {
					return __makeError("unsupported action: " + action);
				}
			} else {
				let nargs = spec->nargs;
				let values = [];
				if (nargs == "?") {
					if (split->hasValue) {
						values = [split->value];
					} else if (argIndex + 1 < input->length && !__isOptionToken(parser, input[argIndex + 1])) {
						argIndex++;
						values = [input[argIndex]];
					}
				} else if (nargs == "*" || nargs == "+") {
					if (split->hasValue) {
						values = [split->value];
					}
					loop (argIndex + 1 < input->length && !__isOptionToken(parser, input[argIndex + 1])) {
						argIndex++;
						values = [...values, input[argIndex]];
					}
					if (nargs == "+" && values->length == 0) {
						return __makeError("argument " + split->name + ": expected at least one argument");
					}
				} else {
					let wanted = nargs;
					if (wanted == null) {
						wanted = 1;
					}
					if (split->hasValue) {
						if (wanted != 1) {
							return __makeError("argument " + split->name + ": ignored explicit argument '" + split->value + "'");
						}
						values = [split->value];
					} else {
						loop (let consumed = 0; consumed < wanted; consumed++) {
							if (argIndex + 1 >= input->length || __isOptionToken(parser, input[argIndex + 1])) {
								if (wanted == 1) {
									return __makeError("argument " + split->name + ": expected one argument");
								}
								return __makeError("argument " + split->name + ": expected " + wanted + " arguments");
							}
							argIndex++;
							values = [...values, input[argIndex]];
						}
					}
				}
				let converted = __convertAll(spec, values);
				if (converted->error != null) {
					return __makeError("argument " + split->name + ": " + converted->error);
				}
				let storedValue = null;
				if (nargs != null && nargs != "?") {
					storedValue = converted->values;
				} else if (converted->values->length > 0) {
					storedValue = converted->values[0];
				} else if (nargs == "?") {
					storedValue = spec["const"];
				}
				if (action == "append") {
					namespace[spec->dest] = [...namespace[spec->dest], storedValue];
				} else {
					namespace[spec->dest] = storedValue;
				}
			}
			if (pending == null) {
				argIndex++;
			}
			continue;
		}

		if (!positionalOnly && __isOptionToken(parser, token)) {
			unknown = [...unknown, token];
			argIndex++;
			continue;
		}
		positionalTokens = [...positionalTokens, token];
		argIndex++;
	}

	let assigned = __assignPositionals(parser, positionalTokens, namespace);
	if (assigned->error != null) {
		return __makeError(assigned->error);
	}
	let missing = assigned->missing;
	loop (let specIndex = 0; specIndex < parser->options->length; specIndex++) {
		let spec = parser->options[specIndex];
		if (spec->required == true && seen[spec->dest] != true) {
			missing = [...missing, __joinWords(spec->flags, "/")];
		}
	}
	if (missing->length > 0) {
		if (!allowUnknown) {
			println(__formatHelp(parser));
		}
		return __makeError("the following arguments are required: " + __joinWords(missing, ", "));
	}
	loop (let index = 0; index < assigned->extras->length; index++) {
		unknown = [...unknown, assigned->extras[index]];
	}
	if (allowUnknown) {
		return {namespace: namespace, unknown: unknown};
	}
	if (unknown->length > 0) {
		return __makeError("unrecognized arguments: " + __joinWords(unknown, " "));
	}
	return namespace;
};

var __Parser::class {
	constructor: fn() {
		this->prog = "program";
		this->description = "";
		this->allow_abbrev = true;
		this->options = [];
		this->positionals = [];
	},
	// Register an option definition (flags) or a positional definition (name/dest).
	add_argument: fn(definition) {
		if (definition->flags != null) {
			if (definition->dest == null) {
				definition->dest = __deriveDest(definition->flags);
			}
			this->options = [...this->options, definition];
		} else {
			if (definition->dest == null) {
				definition->dest = definition->name;
			}
			if (definition->dest == null) {
				return __makeError("argument definition requires flags, name, or dest");
			}
			this->positionals = [...this->positionals, definition];
		}
		return definition;
	},
	// Return the usage and argument description without printing it.
	format_help: fn() {
		return __formatHelp(this);
	},
	// Return the formatted help text. This currently does not write to stdout.
	print_help: fn() {
		return __formatHelp(this);
	},
	// Parse the provided array, or argv when omitted; return namespace or {error}.
	parse_args: fn(args...) {
		let input = argv;
		if (args->length > 0) {
			input = args[0];
		}
		return __parseInternal(this, input, false);
	},
	// Parse like parse_args, but return unrecognized tokens in an unknown array.
	parse_known_args: fn(args...) {
		let input = argv;
		if (args->length > 0) {
			input = args[0];
		}
		return __parseInternal(this, input, true);
	}
};

// Create a configured parser and add -h/--help unless add_help is false.
var ArgumentParser::fn(config...) {
	let parser = __Parser();
	let addHelp = true;
	if (config->length > 0) {
		let settings = config[0];
		if (settings->prog != null) {
			parser->prog = settings->prog;
		}
		if (settings->description != null) {
			parser->description = settings->description;
		}
		if (settings->allow_abbrev != null) {
			parser->allow_abbrev = settings->allow_abbrev;
		}
		if (settings->add_help != null) {
			addHelp = settings->add_help;
		}
	}
	if (addHelp) {
		parser->add_argument({flags: ["-h", "--help"], action: "help", help: "show this help message and exit"});
	}
	return parser;
};

// Construct the standard error-result object used by the parser.
var ArgumentError::fn(message) {
	return {error: message};
};