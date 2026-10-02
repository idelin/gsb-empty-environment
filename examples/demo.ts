import { ReteNetwork } from "../src/index.ts";

const network = new ReteNetwork();

// Rule: an adult user with a large order for that user.
network.addRule({
  name: "adult-with-big-order",
  conditions: [
    {
      type: "User",
      where: { age: { gte: 18 } },
      bind: { name: "?user" },
    },
    {
      type: "Order",
      where: { amount: { gt: 50 } },
      bind: { user: "?user", id: "?orderId" },
    },
  ],
});

const show = (label: string) => {
  const conflictSet = network.getConflictSet();
  console.log(`${label}: conflict set = ${conflictSet.length} activation(s)`);
  for (const activation of conflictSet) {
    console.log(
      `  [fire] ${activation.rule}`,
      Object.fromEntries(activation.bindings),
    );
  }
};

show("initial");

const userId = network.addFact({ type: "User", attrs: { name: "alice", age: 25 } });
show("after User(age=25)");

const orderId = network.addFact({
  type: "Order",
  attrs: { id: "O-1", user: "alice", amount: 100 },
});
show("after Order(amount=100)");

network.retractFact(orderId);
show("after retract Order");

network.retractFact(userId);
show("after retract User");
