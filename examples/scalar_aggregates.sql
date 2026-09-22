SELECT
    customers.name AS customer,
    (
        SELECT COUNT(*)
        FROM payments
        WHERE payments.customer_id = customers.id
    ) AS payment_count,
    (
        SELECT SUM(payments.amount)
        FROM payments
        WHERE payments.customer_id = customers.id
    ) AS total_amount
FROM customers;
