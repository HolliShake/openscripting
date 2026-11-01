import { argv } from "core:sys";
import { println } from "core:io";
import { parseInt, parseFloat } from "core:number";
import { length, codePointAt, fromCodePoint } from "core:unicode";

var makeError::fn(message) {
	return {error: message};
};

var substring::fn(value, start) {
	let output = "";
	loop (let index = start; index < length(value); index++) {
		output += fromCodePoint(codePointAt(value, index));
	}
	return output;
};

var splitOption::fn(token) {
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

var startsWith::fn(value, prefix) {
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

var containsSpace::fn(value) {
	loop (let index = 0; index < length(value); index++) {
		if (codePointAt(value, index) == 32) {
			return true;
		}
	}
	return false;
};

// "-5", "-1.5", "-.5"
var isNegativeNumber::fn(token) {
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

var findExact::fn(parser, name) {
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

var hasNegativeLookingOption::fn(parser) {
	loop (let specIndex = 0; specIndex < parser->options->length; specIndex++) {
		let spec = parser->options[specIndex];
		loop (let nameIndex = 0; nameIndex < spec->flags->length; nameIndex++) {
			if (isNegativeNumber(spec->flags[nameIndex])) {
				return true;
			}
		}
	}
	return false;
};

var isOptionToken::fn(parser, token) {
	if (length(token) < 2 || codePointAt(token, 0) != 45) {
		return false;
	}
	if (token == "--") {
		return true;
	}
	let split = splitOption(token);
	if (findExact(parser, split->name) != null) {
		return true;
	}
	if (isNegativeNumber(token)) {
		return hasNegativeLookingOption(parser);
	}
	if (containsSpace(token)) {
		return false;
	}
	return true;
};

var actionOf::fn(spec) {
	if (spec->action == null) {
		return "store";
	}
	return spec->action;
};

var takesValue::fn(spec) {
	let action = actionOf(spec);
	return action == "store" || action == "append";
};

var deriveDest::fn(flags) {
	let chosen = flags[0];
	loop (let index = 0; index < flags->length; index++) {
		if (startsWith(flags[index], "--")) {
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

var convertValue::fn(value, valueType) {
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

var checkChoices::fn(spec, value) {
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
var convertAll::fn(spec, raw) {
	let out = [];
	loop (let index = 0; index < raw->length; index++) {
		let converted = convertValue(raw[index], spec->type);
		if (converted->error != null) {
			return {values: null, error: converted->error};
		}
		let choiceError = checkChoices(spec, converted->value);
		if (choiceError != null) {
			return {values: null, error: choiceError};
		}
		out = [...out, converted->value];
	}
	return {values: out, error: null};
};

var matchOption::fn(parser, token) {
	let exact = findExact(parser, token);
	if (exact != null) {
		return {spec: exact, ambiguous: false};
	}
	let abbreviation = null;
	let ambiguous = false;
	if (parser->allow_abbrev && length(token) > 2 && startsWith(token, "--")) {
		loop (let specIndex = 0; specIndex < parser->options->length; specIndex++) {
			let spec = parser->options[specIndex];
			loop (let nameIndex = 0; nameIndex < spec->flags->length; nameIndex++) {
				let flag = spec->flags[nameIndex];
				if (startsWith(flag, token)) {
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

var positionalLabel::fn(positional) {
	if (positional->metavar != null) {
		return positional->metavar;
	}
	return positional->dest;
};

var formatHelp::fn(parser) {
	let usage = "usage: " + parser->prog;
	if (parser->options->length > 0) {
		usage += " [options]";
	}
	loop (let index = 0; index < parser->positionals->length; index++) {
		usage += " " + positionalLabel(parser->positionals[index]);
	}
	let output = usage + "\n";
	if (parser->description != "") {
		output += "\n" + parser->description + "\n";
	}
	if (parser->positionals->length > 0) {
		output += "\npositional arguments:\n";
		loop (let index = 0; index < parser->positionals->length; index++) {
			let positional = parser->positionals[index];
			output += "  " + positionalLabel(positional);
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
			if (takesValue(spec)) {
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

var minCount::fn(positional) {
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
var assignPositionals::fn(parser, tokens, namespace) {
	let positionals = parser->positionals;
	let used = 0;
	let missing = [];
	loop (let index = 0; index < positionals->length; index++) {
		let positional = positionals[index];
		let nargs = positional->nargs;
		let reserve = 0;
		loop (let later = index + 1; later < positionals->length; later++) {
			reserve += minCount(positionals[later]);
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
			missing = [...missing, positionalLabel(positional)];
			continue;
		}
		let raw = [];
		loop (let offset = 0; offset < take; offset++) {
			raw = [...raw, tokens[used + offset]];
		}
		used += take;
		let converted = convertAll(positional, raw);
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

var joinWords::fn(words, separator) {
	let out = "";
	loop (let index = 0; index < words->length; index++) {
		if (index > 0) {
			out += separator;
		}
		out += words[index];
	}
	return out;
};

var parseInternal::fn(parser, input, allowUnknown) {
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
		let action = actionOf(spec);
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

		let split = splitOption(token);
		let spec = null;
		if (!positionalOnly && isOptionToken(parser, token)) {
			let match = matchOption(parser, split->name);
			if (match->ambiguous) {
				return makeError("ambiguous option: " + split->name + " could match multiple options");
			}
			spec = match->spec;
			// short option with attached value or cluster: -n5, -abc
			if (spec == null && length(split->name) > 2 && codePointAt(split->name, 1) != 45) {
				let first = "-" + fromCodePoint(codePointAt(split->name, 1));
				let shortSpec = findExact(parser, first);
				if (shortSpec != null) {
					spec = shortSpec;
					let rest = substring(token, 2);
					if (takesValue(shortSpec)) {
						split = {name: first, value: rest, hasValue: true};
					} else {
						split = {name: first, value: "", hasValue: false};
						pending = "-" + rest;
					}
				}
			}
		}

		if (spec != null) {
			let action = actionOf(spec);
			seen[spec->dest] = true;
			if (action == "help") {
				return {help: formatHelp(parser), namespace: namespace};
			}
			if (!takesValue(spec)) {
				if (split->hasValue) {
					return makeError("argument " + split->name + ": ignored explicit argument '" + split->value + "'");
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
					return makeError("unsupported action: " + action);
				}
			} else {
				let nargs = spec->nargs;
				let values = [];
				if (nargs == "?") {
					if (split->hasValue) {
						values = [split->value];
					} else if (argIndex + 1 < input->length && !isOptionToken(parser, input[argIndex + 1])) {
						argIndex++;
						values = [input[argIndex]];
					}
				} else if (nargs == "*" || nargs == "+") {
					if (split->hasValue) {
						values = [split->value];
					}
					loop (argIndex + 1 < input->length && !isOptionToken(parser, input[argIndex + 1])) {
						argIndex++;
						values = [...values, input[argIndex]];
					}
					if (nargs == "+" && values->length == 0) {
						return makeError("argument " + split->name + ": expected at least one argument");
					}
				} else {
					let wanted = nargs;
					if (wanted == null) {
						wanted = 1;
					}
					if (split->hasValue) {
						if (wanted != 1) {
							return makeError("argument " + split->name + ": ignored explicit argument '" + split->value + "'");
						}
						values = [split->value];
					} else {
						loop (let consumed = 0; consumed < wanted; consumed++) {
							if (argIndex + 1 >= input->length || isOptionToken(parser, input[argIndex + 1])) {
								if (wanted == 1) {
									return makeError("argument " + split->name + ": expected one argument");
								}
								return makeError("argument " + split->name + ": expected " + wanted + " arguments");
							}
							argIndex++;
							values = [...values, input[argIndex]];
						}
					}
				}
				let converted = convertAll(spec, values);
				if (converted->error != null) {
					return makeError("argument " + split->name + ": " + converted->error);
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

		if (!positionalOnly && isOptionToken(parser, token)) {
			unknown = [...unknown, token];
			argIndex++;
			continue;
		}
		positionalTokens = [...positionalTokens, token];
		argIndex++;
	}

	let assigned = assignPositionals(parser, positionalTokens, namespace);
	if (assigned->error != null) {
		return makeError(assigned->error);
	}
	let missing = assigned->missing;
	loop (let specIndex = 0; specIndex < parser->options->length; specIndex++) {
		let spec = parser->options[specIndex];
		if (spec->required == true && seen[spec->dest] != true) {
			missing = [...missing, joinWords(spec->flags, "/")];
		}
	}
	if (missing->length > 0) {
		if (!allowUnknown) {
			println(formatHelp(parser));
		}
		return makeError("the following arguments are required: " + joinWords(missing, ", "));
	}
	loop (let index = 0; index < assigned->extras->length; index++) {
		unknown = [...unknown, assigned->extras[index]];
	}
	if (allowUnknown) {
		return {namespace: namespace, unknown: unknown};
	}
	if (unknown->length > 0) {
		return makeError("unrecognized arguments: " + joinWords(unknown, " "));
	}
	return namespace;
};

var Parser::class {
	constructor: fn() {
		this->prog = "program";
		this->description = "";
		this->allow_abbrev = true;
		this->options = [];
		this->positionals = [];
	},
	add_argument: fn(definition) {
		if (definition->flags != null) {
			if (definition->dest == null) {
				definition->dest = deriveDest(definition->flags);
			}
			this->options = [...this->options, definition];
		} else {
			if (definition->dest == null) {
				definition->dest = definition->name;
			}
			if (definition->dest == null) {
				return makeError("argument definition requires flags, name, or dest");
			}
			this->positionals = [...this->positionals, definition];
		}
		return definition;
	},
	format_help: fn() {
		return formatHelp(this);
	},
	print_help: fn() {
		return formatHelp(this);
	},
	parse_args: fn(args...) {
		let input = argv;
		if (args->length > 0) {
			input = args[0];
		}
		return parseInternal(this, input, false);
	},
	parse_known_args: fn(args...) {
		let input = argv;
		if (args->length > 0) {
			input = args[0];
		}
		return parseInternal(this, input, true);
	}
};

var ArgumentParser::fn(config...) {
	let parser = Parser();
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

var ArgumentError::fn(message) {
	return {error: message};
};