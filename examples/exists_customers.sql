SELECT customers.name
FROM customers
WHERE EXISTS (
    SELECT *
    FROM payments
    WHERE payments.customer_id = customers.id
);
