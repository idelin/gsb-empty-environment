"""resource_checker: resource-ownership invariant checker for finite-state models.

Verifies that on every normal, exceptional and cancellation exit of a given
finite-state model, every acquired resource is released exactly once by its
current owner.  Ownership is tracked per resource (no global reference
counts); transfer moves ownership and invalidates the previous owner.
"""
