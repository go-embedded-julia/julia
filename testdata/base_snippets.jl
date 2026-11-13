[@async 1 for i in 1:1]
#---
map(x->x^2, 1:4)
#---
[i^2 for i in 1:4]
#---
let ts = [@async i^2 for i in 1:4]
    map(fetch, ts)
end
