n = int(input())
co = 0
for i in range(1, n + 1):
	if n % i == 0:
		co += 1
if co == 2:
	print(f"{n} adalah PRIMA")
else:
	print(f"{n} adalah TIDAK PRIMA")