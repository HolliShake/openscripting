var items = [10, 20];
log items[1]++ + 2;
log items[1];
log ++items[0];
log items[0];

var evaluations = 0;
var getItems::fn() {
	evaluations++;
	return items;
};
var getIndex::fn() {
	evaluations++;
	return 0;
};
log getItems()[getIndex()]++;
log evaluations;
log items[0];

evaluations = 0;
log ++getItems()[getIndex()];
log evaluations;
log items[0];

evaluations = 0;
log getItems()[getIndex()]--;
log evaluations;
log items[0];

evaluations = 0;
log --getItems()[getIndex()];
log evaluations;
log items[0];
