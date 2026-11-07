var counter = 0;
var sequence::fn*() {
	counter++;
	yield counter;
	counter++;
	yield counter;
	return 9;
};

var iterator = sequence();
log(counter);
log(iterator->next()->value);
log(counter);
log(iterator->next()->value);
log(iterator->next()->value);
log(iterator->next()->done);
