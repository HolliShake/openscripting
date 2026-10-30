var matched = switch(2) {
    1 => "first",
    2, 3 => "matched",
    default => "fallback"
};

var fallback = switch(9) {
    1 => "first",
    default => "fallback"
};

var nullCase = switch(null) {
    null => "null case",
    default => "fallback"
};

log matched;
log fallback;
log nullCase;
