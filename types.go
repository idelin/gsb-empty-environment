package mealsolver

// 求解结果状态。
const (
	StatusOptimal    = "optimal"
	StatusInfeasible = "infeasible"
	StatusError      = "error"
)

// Bound 为某营养素每日必须落入的整数区间 [Min, Max]。
// 约束按“每日”分别生效，不能用多日平均值替代。
type Bound struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Tier 为菜品的一个份量档位，营养与成本按该档位整体给出（整数）。
type Tier struct {
	Name      string         `json:"name"`
	Nutrition map[string]int `json:"nutrition"`
	Cost      int            `json:"cost"`
}

// Dish 为一道可选菜品。
type Dish struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Allergens []string `json:"allergens,omitempty"`
	Tiers     []Tier   `json:"tiers"`
}

// Config 为求解配置。零值字段在 withDefaults 中取默认值。
type Config struct {
	Days                 int              `json:"days"`           // 默认 5
	MealsPerDay          int              `json:"mealsPerDay"`    // 默认 3
	NutrientBounds       map[string]Bound `json:"nutrientBounds"` // 每日营养整数区间
	Allergens            []string         `json:"allergens,omitempty"`
	MaxConsecutiveRepeat int              `json:"maxConsecutiveRepeat"` // 同一道菜最多连续出现的天数，<=0 表示不限制
	MaxDishes            int              `json:"maxDishes"`            // 全程不同菜品总数上限，默认 20
}

func (c Config) withDefaults() Config {
	if c.Days <= 0 {
		c.Days = 5
	}
	if c.MealsPerDay <= 0 {
		c.MealsPerDay = 3
	}
	if c.MaxDishes <= 0 {
		c.MaxDishes = 20
	}
	if c.MaxConsecutiveRepeat <= 0 || c.MaxConsecutiveRepeat > c.Days {
		c.MaxConsecutiveRepeat = c.Days
	}
	return c
}

// MealChoice 为某一天某一餐位的选择。
type MealChoice struct {
	Day      int    `json:"day"`
	Meal     int    `json:"meal"`
	DishID   string `json:"dishId"`
	DishName string `json:"dishName"`
	Tier     string `json:"tier"`
}

// Result 为求解输出。无解时 Conflicts 给出一个极小冲突约束子集。
type Result struct {
	Status      string           `json:"status"`
	TotalCost   int              `json:"totalCost,omitempty"`
	Choices     []MealChoice     `json:"choices,omitempty"`
	DailyTotals []map[string]int `json:"dailyTotals,omitempty"`
	Conflicts   []string         `json:"conflicts,omitempty"`
	Err         string           `json:"error,omitempty"`
}
