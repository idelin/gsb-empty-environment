export function stableSort<T>(items: T[], compare: (a: T, b: T) => number): T[] {
  const arr = items.slice();
  const n = arr.length;
  if (n < 2) {
    return arr;
  }
  const tmp: T[] = new Array(n);
  for (let width = 1; width < n; width *= 2) {
    for (let start = 0; start < n; start += 2 * width) {
      const mid = Math.min(start + width, n);
      const end = Math.min(start + 2 * width, n);
      let i = start;
      let j = mid;
      let k = start;
      while (i < mid && j < end) {
        if (compare(arr[j], arr[i]) < 0) {
          tmp[k] = arr[j];
          j += 1;
        } else {
          tmp[k] = arr[i];
          i += 1;
        }
        k += 1;
      }
      while (i < mid) {
        tmp[k] = arr[i];
        i += 1;
        k += 1;
      }
      while (j < end) {
        tmp[k] = arr[j];
        j += 1;
        k += 1;
      }
    }
    for (let t = 0; t < n; t += 1) {
      arr[t] = tmp[t];
    }
  }
  return arr;
}

export function compareLabels(a: string, b: string): number {
  const numA = Number(a);
  const numB = Number(b);
  const aNumeric = a.trim() !== '' && !Number.isNaN(numA);
  const bNumeric = b.trim() !== '' && !Number.isNaN(numB);
  if (aNumeric && bNumeric) {
    if (numA < numB) {
      return -1;
    }
    if (numA > numB) {
      return 1;
    }
    return 0;
  }
  if (a < b) {
    return -1;
  }
  if (a > b) {
    return 1;
  }
  return 0;
}
