package suggest

// damerauLevenshtein computes the optimal-string-alignment distance:
// insertions, deletions, substitutions and adjacent transpositions each cost 1.
func damerauLevenshtein(a, b string) int {
	left, right := []rune(a), []rune(b)
	rows, cols := len(left)+1, len(right)+1

	distance := make([][]int, rows)
	for i := range distance {
		distance[i] = make([]int, cols)
		distance[i][0] = i
	}
	for j := 0; j < cols; j++ {
		distance[0][j] = j
	}

	for i := 1; i < rows; i++ {
		for j := 1; j < cols; j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}
			best := min(distance[i-1][j]+1, distance[i][j-1]+1, distance[i-1][j-1]+cost)
			if i > 1 && j > 1 && left[i-1] == right[j-2] && left[i-2] == right[j-1] {
				best = min(best, distance[i-2][j-2]+1)
			}
			distance[i][j] = best
		}
	}
	return distance[rows-1][cols-1]
}

func similarity(a, b string) float64 {
	longest := max(len([]rune(a)), len([]rune(b)))
	if longest == 0 {
		return 1
	}
	return 1 - float64(damerauLevenshtein(a, b))/float64(longest)
}
