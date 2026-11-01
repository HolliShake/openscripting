import { argv } from "core:sys";
import { parseInt, parseFloat } from "core:number";
import { length, codePointAt, fromCodePoint } from "core:unicode";

var makeError::fn(message) {
	return {error: message};
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

var isOptionToken::fn(token) {
	return length(token) > 0 && codePointAt(token, 0) == 45;
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

var matchOption::fn(parser, token) {
	let exact = null;
	let abbreviation = null;
	let ambiguous = false;
	loop (let specIndex = 0; specIndex < parser->options->length; specIndex++) {
		let spec = parser->options[specIndex];
		loop (let nameIndex = 0; nameIndex < spec->flags->length; nameIndex++) {
			let flag = spec->flags[nameIndex];
			if (token == flag) {
				exact = spec;
			} else if (parser->allow_abbrev && startsWith(flag, token) && length(token) > 2 &&
				codePointAt(token, 0) == 45 && codePointAt(token, 1) == 45) {
				if (abbreviation != null && abbreviation->dest != spec->dest) {
					ambiguous = true;
				}
				abbreviation = spec;
			}
		}
	}
	if (exact != null) {
		return {spec: exact, ambiguous: false};
	}
	return {spec: abbreviation, ambiguous: ambiguous};
};

var formatHelp::fn(parser) {
	let usage = "usage: " + parser->prog;
	loop (let index = 0; index < parser->positionals->length; index++) {
		let positional = parser->positionals[index];
		let label = positional->metavar;
		if (label == null) {
			label = positional->dest;
		}
		usage += " " + label;
	}
	if (parser->options->length > 0) {
		usage += " [options]";
	}
	let output = usage + "\n";
	if (parser->description != "") {
		output += "\n" + parser->description + "\n";
	}
	if (parser->positionals->length > 0) {
		output += "\npositional arguments:\n";
		loop (let index = 0; index < parser->positionals->length; index++) {
			let positional = parser->positionals[index];
			output += "  " + positional->dest;
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
			if (spec->action != "store_true" && spec->action != "store_false" && spec->action != "count") {
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

var parseInternal::fn(parser, input, allowUnknown) {
	if (input == null) {
		input = argv;
	}
	let namespace = {};
	let seen = {};
	let unknown = [];
	let positionalIndex = 0;
	let positionalValues = [];
	let positionalOnly = false;

	loop (let specIndex = 0; specIndex < parser->options->length; specIndex++) {
		let spec = parser->options[specIndex];
		let action = spec->action;
		if (action == null) {
			action = "store";
		}
		let defaultValue = spec["default"];
		if (defaultValue == null) {
			if (action == "store_true") {
				defaultValue = false;
			} else if (action == "store_false") {
				defaultValue = true;
			} else if (action == "count") {
				defaultValue = 0;
			} else if (action == "append") {
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
		if (!positionalOnly && token == "--") {
			positionalOnly = true;
			argIndex++;
			continue;
		}

		let split = splitOption(token);
		let match = {spec: null, ambiguous: false};
		if (!positionalOnly && isOptionToken(token)) {
			match = matchOption(parser, split->name);
		}
		if (match->ambiguous) {
			return makeError("ambiguous option: " + split->name);
		}
		if (match->spec != null) {
			let spec = match->spec;
			let action = spec->action;
			if (action == null) {
				action = "store";
			}
			seen[spec->dest] = true;
			if (action == "help") {
				return {help: formatHelp(parser), namespace: namespace};
			} else if (action == "store_true") {
				namespace[spec->dest] = true;
			} else if (action == "store_false") {
				namespace[spec->dest] = false;
			} else if (action == "count") {
				namespace[spec->dest]++;
			} else if (action == "store" || action == "append") {
				let nargs = spec->nargs;
				if (nargs == null) {
					nargs = 1;
				}
				let values = [];
				let wanted = nargs;
				if (nargs == "?") {
					wanted = 1;
					if (split->hasValue) {
						values = [split->value];
					} else if (argIndex + 1 < input->length && !isOptionToken(input[argIndex + 1])) {
						argIndex++;
						values = [input[argIndex]];
					} else if (spec["const"] != null) {
						values = [spec["const"]];
					}
				} else if (nargs == "*" || nargs == "+") {
					if (split->hasValue) {
						values = [split->value];
					}
					loop (argIndex + 1 < input->length && !isOptionToken(input[argIndex + 1])) {
						argIndex++;
						values = [...values, input[argIndex]];
					}
					if (nargs == "+" && values->length == 0) {
						return makeError("expected at least one argument for " + split->name);
					}
				} else {
					if (split->hasValue) {
						values = [split->value];
					} else {
						loop (let consumed = 0; consumed < wanted; consumed++) {
							if (argIndex + 1 >= input->length || isOptionToken(input[argIndex + 1])) {
								return makeError("expected argument for " + split->name);
							}
							argIndex++;
							values = [...values, input[argIndex]];
						}
					}
				}
				let convertedValues = [];
				loop (let valueIndex = 0; valueIndex < values->length; valueIndex++) {
					let converted = convertValue(values[valueIndex], spec->type);
					if (converted->error != null) {
						return makeError(converted->error);
					}
					if (spec->choices != null) {
						let accepted = false;
						loop (let choiceIndex = 0; choiceIndex < spec->choices->length; choiceIndex++) {
							if (converted->value == spec->choices[choiceIndex]) {
								accepted = true;
							}
						}
						if (!accepted) {
							return makeError("invalid choice: " + converted->value);
						}
					}
					convertedValues = [...convertedValues, converted->value];
				}
				let storedValue = null;
				if (nargs == "*" || nargs == "+" || (nargs != "?" && wanted != 1)) {
					storedValue = convertedValues;
				} else if (convertedValues->length > 0) {
					storedValue = convertedValues[0];
				} else if (nargs == "?" && spec["const"] != null) {
					storedValue = spec["const"];
				}
				if (action == "append") {
					namespace[spec->dest] = [...namespace[spec->dest], storedValue];
				} else {
					namespace[spec->dest] = storedValue;
				}
			} else {
				return makeError("unsupported action: " + action);
			}
			argIndex++;
			continue;
		}

		if (!positionalOnly && isOptionToken(token)) {
			if (allowUnknown) {
				unknown = [...unknown, token];
				argIndex++;
				continue;
			}
			return makeError("unrecognized argument: " + token);
		}
		if (positionalIndex >= parser->positionals->length) {
			if (allowUnknown) {
				unknown = [...unknown, token];
				argIndex++;
				continue;
			}
			return makeError("unrecognized argument: " + token);
		}
		let positional = parser->positionals[positionalIndex];
		let positionalNargs = positional->nargs;
		if (positionalNargs == null) {
			positionalNargs = 1;
		}
		let convertedPositional = convertValue(token, positional->type);
		if (convertedPositional->error != null) {
			return makeError(convertedPositional->error);
		}
		if (positionalNargs == "*" || positionalNargs == "+") {
			positionalValues = [...positionalValues, convertedPositional->value];
			namespace[positional->dest] = positionalValues;
		} else {
			namespace[positional->dest] = convertedPositional->value;
			positionalIndex++;
		}
		argIndex++;
	}

	loop (let specIndex = 0; specIndex < parser->options->length; specIndex++) {
		let spec = parser->options[specIndex];
		if (spec->required == true && seen[spec->dest] != true) {
			return makeError("required argument missing: " + spec->dest);
		}
	}
	loop (let specIndex = 0; specIndex < parser->positionals->length; specIndex++) {
		let spec = parser->positionals[specIndex];
		let nargs = spec->nargs;
		if (namespace[spec->dest] == null && nargs != "?" && nargs != "*") {
			return makeError("required argument missing: " + spec->dest);
		}
	}
	if (allowUnknown) {
		return {namespace: namespace, unknown: unknown};
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
		if (definition->dest == null) {
			return makeError("argument definition requires dest");
		}
		if (definition->flags != null) {
			this->options = [...this->options, definition];
		} else {
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
	}
	return parser;
};

var ArgumentError::fn(message) {
	return {error: message};
};
