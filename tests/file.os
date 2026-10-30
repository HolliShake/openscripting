
var items = [
    "One",
    0,
    "Three"
];

var objc = {
    A: 100,
    B: 100
};


log items;
items[1] += 1000 + 2;
objc->A  += 4000 + 0;
items[1]++;
log items;
log objc;

