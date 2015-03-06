OPTIMIZATION?=-O0
DEBUG?=-g -ggdb -rdynamic

all: nstar

clean:
	rm -f *.o nstar.real

.PHONY: all clean

nstar: nstar.o pwd.o
	$(CC) -static -o $@.real $^

%.o: %.c
	$(CC) -c -Wall $(OPTIMIZATION) $(DEBUG) $(CFLAGS) $<
